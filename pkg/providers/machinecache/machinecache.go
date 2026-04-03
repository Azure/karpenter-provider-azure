package machinecache

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	// DefaultMachineCacheTTL is the default time-to-live for machine list cache entries.
	// GET Machine 429s at 1K-node scale cost 17-29 seconds each; caching LIST results
	// converts O(N) individual GETs into O(1) cached lookups. A 30-second TTL is
	// acceptable because drift and reconciliation checks re-run on subsequent cycles anyway.
	DefaultMachineCacheTTL = 30 * time.Second

	// Provisioning state constants for AKS Machine API
	ProvisioningStateCreating  = "Creating"
	ProvisioningStateUpdating  = "Updating"
	ProvisioningStateDeleting  = "Deleting"
	ProvisioningStateSucceeded = "Succeeded"
	ProvisioningStateFailed    = "Failed"
)

var (
	nodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

type AKSMachineNewListPager interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
}

type cacheEntry struct {
	machine     *armcontainerservice.Machine
	lastUpdated time.Time
}

// MachineCache caches AKS machines to reduce API calls and improve performance.
type MachineCache struct {
	machines             sync.Map     // keyed by machine name (not full ARM ID), value is *armcontainerservice.Machine
	lastUpdatedUnixNanos atomic.Int64 // nanoseconds since epoch; 0 means never updated
	ttl                  time.Duration
	interval             time.Duration
	client               AKSMachineNewListPager

	clusterResourceGroup string
	clusterName          string
	aksMachinesPoolName  string

	// Retry configuration
	maxRetries    int
	retryDelay    time.Duration
	maxRetryDelay time.Duration

	// Background worker fields
	updateRequests chan struct{}      // Buffered channel (size 1) for update requests
	workerCtx      context.Context    // Worker lifecycle context
	workerCancel   context.CancelFunc // Cancel function for shutdown
	updateInterval time.Duration      // Periodic update frequency (default 5 minutes)
	wg             sync.WaitGroup     // Tracks worker goroutine for shutdown
}

func NewMachineCache(ctx context.Context, ttl time.Duration, client AKSMachineNewListPager, interval time.Duration, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineCache {
	workerCtx, workerCancel := context.WithCancel(ctx)

	cache := &MachineCache{
		// machines is a sync.Map, zero value is ready to use
		ttl:                  ttl,
		interval:             interval,
		client:               client,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
		maxRetries:           500, // generous retries for now
		retryDelay:           1 * time.Second,
		maxRetryDelay:        30 * time.Second,

		// Initialize background worker
		updateRequests: make(chan struct{}, 1), // Buffer of 1 coalesces requests
		workerCtx:      workerCtx,
		workerCancel:   workerCancel,
		updateInterval: 5 * time.Minute, // Periodic refresh every 5 minutes
	}

	// Start background worker
	cache.wg.Add(1)
	go cache.updateWorker()

	return cache
}

// isFresh returns true if the cache has been populated and hasn't expired.
// Lock-free implementation using atomic operations for better concurrency.
func (c *MachineCache) isFresh() bool {
	lastUpdatedNanos := c.lastUpdatedUnixNanos.Load()
	if lastUpdatedNanos == 0 {
		return false
	}
	lastUpdated := time.Unix(0, lastUpdatedNanos)
	return time.Since(lastUpdated) < c.ttl
}

// Get retrieves a machine from the cache by name.
func (c *MachineCache) Get(machineName string) (*armcontainerservice.Machine, error) {
	if !c.isFresh() {
		return nil, fmt.Errorf("cache is stale for machine %q", machineName)
	}

	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, fmt.Errorf("machine %q not found in cache", machineName)
	}

	return value.(*armcontainerservice.Machine), nil
}

// List returns all machines in the cache.
// Returns an error if the cache is not fresh and requests an update.
func (c *MachineCache) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	// Create a slice with all machines
	machines := make([]*armcontainerservice.Machine, 0)
	c.machines.Range(func(key, value any) bool {
		machines = append(machines, value.(*armcontainerservice.Machine))
		return true // continue iteration
	})

	return machines, nil
}

// get retrieves a machine from the cache by name.
func (c *MachineCache) get(machineName string) (*armcontainerservice.Machine, bool) {
	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, false
	}
	return value.(*armcontainerservice.Machine), true
}

// invalidate removes a specific machine from the cache, forcing the next Get()
// for that machine to fall through to the API. This is called after mutating
// operations (Create, Update, Delete) to prevent serving stale data.
func (c *MachineCache) invalidate(machineName string) {
	c.machines.Delete(machineName)
}

// invalidateAll clears the entire cache, forcing all subsequent Get() calls
// to fall through to the API until the next List() repopulates the cache.
func (c *MachineCache) invalidateAll() {
	// Delete all entries by ranging over the map
	c.machines.Range(func(key, value any) bool {
		c.machines.Delete(key)
		return true // continue iteration
	})
	c.lastUpdatedUnixNanos.Store(0) // zero value → isFresh() returns false
}

// updateWorker runs in a background goroutine and handles both periodic and on-demand cache updates.
// It stops when workerCtx is canceled, ensuring clean shutdown via Shutdown().
func (c *MachineCache) updateWorker() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.workerCtx.Done():
			return

		case <-ticker.C:
			// Periodic refresh to keep cache fresh
			if err := c.update(c.workerCtx); err != nil {
				// Log error but continue - next tick will retry
				log.FromContext(c.workerCtx).Error(err, "background cache update failed (periodic)")
			}
		}
	}
}

// Shutdown stops the background update worker and waits for it to finish.
// Call this during provider shutdown to prevent goroutine leaks.
// After calling Shutdown, the cache will no longer receive automatic updates.
func (c *MachineCache) Shutdown() {
	c.workerCancel() // Signal worker to stop
	c.wg.Wait()      // Wait for worker goroutine to finish
}

// PollUntilDone polls the cache for the specified AKS machine until it reaches a terminal state or the context is canceled.
func (c *MachineCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
	log.FromContext(ctx).Info("starting cache poller for AKS machine", "aksMachineName", name, "interval", c.interval.String())
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	var retryAttemptsLeft int
	var currentRetryDelay time.Duration
	c.resetRetryState(&retryAttemptsLeft, &currentRetryDelay)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while polling for AKS machine %q", name)

		case <-ticker.C:
			provisioningErr, pollerErr, done := c.pollOnce(ctx, name, &retryAttemptsLeft, &currentRetryDelay)
			if done {
				return provisioningErr, pollerErr
			}
		}
	}
}

// pollOnce performs a single cache-based poll and returns (provisioningErr, pollerErr, done).
func (c *MachineCache) pollOnce(ctx context.Context, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	// Get machine from cache (may be stale, but worker is updating)
	machine, ok := c.get(aksMachineName)
	if !ok {
		shouldRetry, backoffErr := c.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
		if backoffErr != nil {
			return nil, backoffErr, true
		}
		if shouldRetry {
			return nil, nil, false
		}
		return nil, fmt.Errorf("cache poller: exhausted retries due to repeated cache misses for AKS machine %q", aksMachineName), true
	}

	// Machine found - reset retry state
	c.resetRetryState(retryAttemptsLeft, currentRetryDelay)

	if machine.Properties == nil || machine.Properties.ProvisioningState == nil {
		return c.handleNilProvisioningState(ctx, machine, aksMachineName, retryAttemptsLeft, currentRetryDelay)
	}

	return c.handleProvisioningState(ctx, machine, aksMachineName, retryAttemptsLeft, currentRetryDelay)
}

// handleNilProvisioningState handles the case where the machine's provisioning state is nil.
func (c *MachineCache) handleNilProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	log.FromContext(ctx).V(1).Info("Cache poller: warning: polling for AKS machine found nil provisioning state, may retry",
		"aksMachineName", aksMachineName,
		"aksMachineID", aksMachine.ID,
		"provisioningState", nil,
		"retryAttemptsLeft", *retryAttemptsLeft,
		"retryDelay", *currentRetryDelay,
	)

	shouldRetry, backoffErr := c.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
	if backoffErr != nil {
		return nil, backoffErr, true
	}
	if shouldRetry {
		return nil, nil, false
	}
	return nil, fmt.Errorf("AKS machine %q sees nil provisioning state after exhausting %d retry attempts", aksMachineName, c.maxRetries), true
}

// handleProvisioningState processes the machine's provisioning state and returns the appropriate action.
func (c *MachineCache) handleProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	provisioningState := *aksMachine.Properties.ProvisioningState
	switch provisioningState {
	// Non-terminal states
	case consts.ProvisioningStateCreating, consts.ProvisioningStateUpdating:
		log.FromContext(ctx).V(2).Info("Cache poller: polling for AKS machine ongoing",
			"aksMachineName", aksMachineName,
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
		)
		// Reset retry counter on healthy non-terminal state (progress is being made)
		c.resetRetryState(retryAttemptsLeft, currentRetryDelay)
		return nil, nil, false

	// Canceled terminal state
	case consts.ProvisioningStateDeleting:
		return nil, fmt.Errorf("AKS machine %q sees canceled provisioning state %s", aksMachineName, provisioningState), true

	// Succeeded terminal state
	case consts.ProvisioningStateSucceeded:
		return nil, nil, true

	// Fatal terminal state
	case consts.ProvisioningStateFailed:
		if aksMachine.Properties.Status != nil && aksMachine.Properties.Status.ProvisioningError != nil {
			return aksMachine.Properties.Status.ProvisioningError, nil, true
		}
		return nil, fmt.Errorf("AKS machine %q sees fatal provisioning state %s, but ProvisioningError is nil", aksMachineName, provisioningState), true

	// Unrecognized state
	default:
		log.FromContext(ctx).V(1).Info("Cache poller: warning: polling for AKS machine found unrecognized provisioning state, may retry",
			"aksMachineName", aksMachineName,
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
			"retryAttemptsLeft", *retryAttemptsLeft,
			"retryDelay", *currentRetryDelay,
		)

		shouldRetry, backoffErr := c.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
		if backoffErr != nil {
			return nil, backoffErr, true
		}
		if shouldRetry {
			return nil, nil, false
		}
		return nil, fmt.Errorf("AKS machine %q sees unrecognized provisioning state %s after exhausting %d retry attempts", aksMachineName, provisioningState, c.maxRetries), true
	}
}

// retryWithBackoff applies exponential backoff and returns true if retry should continue, false if exhausted.
func (c *MachineCache) retryWithBackoff(ctx context.Context, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (shouldRetry bool, err error) {
	if *retryAttemptsLeft <= 0 {
		return false, nil
	}

	*retryAttemptsLeft--

	select {
	case <-time.After(*currentRetryDelay):
		*currentRetryDelay = min(*currentRetryDelay*2, c.maxRetryDelay)
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (c *MachineCache) resetRetryState(retryAttemptsLeft *int, currentRetryDelay *time.Duration) {
	*retryAttemptsLeft = c.maxRetries
	*currentRetryDelay = c.retryDelay
}

func (c *MachineCache) update(ctx context.Context) error {
	now := time.Now()
	defer func() {
		log.FromContext(ctx).Info("finished updating machine list cache",
			"duration", time.Since(now).Seconds(),
			"aksMachinesPoolName", c.aksMachinesPoolName,
		)
	}()

	pager := c.client.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return fmt.Errorf("failed to list AKS machines: created pager is nil")
	}

	fetchedMachineNames := make(map[string]struct{})
	startPage := time.Now()

	for pager.More() {
		pageNow := time.Now()
		page, err := pager.NextPage(ctx)
		log.FromContext(ctx).Info("fetched page of AKS machines",
			"duration", time.Since(pageNow).Seconds(),
			"aksMachinesPoolName", c.aksMachinesPoolName,
		)
		if err != nil {
			if isAKSMachineOrMachinesPoolNotFound(err) {
				log.FromContext(ctx).V(1).Info("failed to list AKS machines: AKS machines pool not found, treating as no AKS machines found")
				break
			}

			return fmt.Errorf("failed to list AKS machines: %w", err)
		}

		log.FromContext(ctx).Info("processing page of AKS machines",
			"pageSize", len(page.Value),
			"aksMachinesPoolName", c.aksMachinesPoolName,
		)

		for _, aksMachine := range page.Value {
			if isValid(ctx, aksMachine) {
				if aksMachine.Name != nil {
					c.machines.Store(*aksMachine.Name, aksMachine)
					fetchedMachineNames[*aksMachine.Name] = struct{}{}
				}
			}

		}

	}

	log.FromContext(ctx).Info("completed LIST of AKS machines",
		"duration", time.Since(startPage).Seconds(),
		"totalMachines", len(fetchedMachineNames),
		"aksMachinesPoolName", c.aksMachinesPoolName,
	)

	c.machines.Range(func(key, value any) bool {
		machineName := key.(string)
		if _, exists := fetchedMachineNames[machineName]; !exists {
			c.machines.Delete(machineName)
		}
		return true
	})

	c.lastUpdatedUnixNanos.Store(time.Now().UnixNano())
	return nil
}

func isValid(ctx context.Context, aksMachine *armcontainerservice.Machine) bool {
	if aksMachine == nil || aksMachine.Properties == nil {
		log.FromContext(ctx).Info("invalid AKS machine: nil properties", "aksMachineName", lo.FromPtr(aksMachine.Name))
		return false
	}
	if aksMachine.Properties.Tags == nil {
		log.FromContext(ctx).Info("invalid AKS machine: nil tags", "aksMachineName", lo.FromPtr(aksMachine.Name))
		return false
	}
	if _, hasKarpenterTag := aksMachine.Properties.Tags[nodePoolTagKey]; !hasKarpenterTag {
		log.FromContext(ctx).Info("invalid AKS machine: missing Karpenter nodepool tag", "aksMachineName", lo.FromPtr(aksMachine.Name))
		return false
	}
	return true
}

func isAKSMachineOrMachinesPoolNotFound(err error) bool {
	if err == nil {
		return false
	}
	azErr := sdkerrors.IsResponseError(err)
	if azErr != nil && (azErr.StatusCode == http.StatusNotFound || // Covers AKS machines pool not found on PUT machine, GET machine, GET (list) machines, POST agent pool (DELETE machines), and AKS machine not found on GET machine
		(azErr.StatusCode == http.StatusBadRequest && azErr.ErrorCode == "InvalidParameter" && strings.Contains(azErr.Error(), "Cannot find any valid machines"))) { // Covers AKS machine not found on POST agent pool (DELETE machines)
		return true
	}
	return false
}
