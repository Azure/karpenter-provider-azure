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

	// ActiveRefreshInterval is the interval at which the cache is refreshed when there are active pollers.
	activeRefreshInterval = 1 * time.Minute
	// BackgroundRefreshInterval is the interval at which the cache is refreshed in the background.
	// backgroundRefreshInterval = 5 * time.Minute
)

var (
	nodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

// AKSMachineNewListPager defines the interface for creating a new pager to list AKS machines.
type AKSMachineNewListPager interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
}

type cacheEntry struct {
	machine           *armcontainerservice.Machine
	lastUpdatedUnixNs atomic.Int64 // Unix nanoseconds timestamp for thread-safe access
}

// MachineCache caches AKS machines to reduce API calls and improve performance.
type MachineCache struct {
	machines     sync.Map // keyed by machine name (not full ARM ID), value is *cacheEntry
	ttl          time.Duration
	client       AKSMachineNewListPager
	pollInterval time.Duration

	clusterResourceGroup string
	clusterName          string
	aksMachinesPoolName  string

	// Retry configuration
	maxRetries    int
	retryDelay    time.Duration
	maxRetryDelay time.Duration

	// Background worker fields
	workerCtx    context.Context    // Worker lifecycle context
	workerCancel context.CancelFunc // Cancel function for shutdown
	wg           sync.WaitGroup     // Tracks worker goroutine for shutdown

	activePollers atomic.Int32
}

func NewMachineCache(ctx context.Context, client AKSMachineNewListPager, ttl, pollInterval time.Duration, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineCache {
	workerCtx, workerCancel := context.WithCancel(ctx)

	cache := &MachineCache{
		// machines is a sync.Map, zero value is ready to use
		ttl:                  ttl,
		client:               client,
		pollInterval:         pollInterval,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
		maxRetries:           500, // generous retries for now
		retryDelay:           1 * time.Second,
		maxRetryDelay:        30 * time.Second,

		// Initialize background worker
		workerCtx:    workerCtx,
		workerCancel: workerCancel,
	}

	// Start background worker
	cache.wg.Add(1)
	go cache.updateWorker()

	return cache
}

// isExpired checks if a specific cache entry is expired (older than 10 minutes).
func (c *MachineCache) isExpired(entry *cacheEntry) bool {
	if entry == nil {
		return true
	}
	lastUpdatedNanos := entry.lastUpdatedUnixNs.Load()
	if lastUpdatedNanos == 0 {
		return true
	}
	lastUpdated := time.Unix(0, lastUpdatedNanos)
	return time.Since(lastUpdated) > 10*time.Minute
}

// Get retrieves a machine from the cache by name.
func (c *MachineCache) Get(machineName string) (*armcontainerservice.Machine, error) {
	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, fmt.Errorf("machine %q not found in cache", machineName)
	}

	entry := value.(*cacheEntry)
	if c.isExpired(entry) {
		lastUpdatedNanos := entry.lastUpdatedUnixNs.Load()
		lastUpdated := time.Unix(0, lastUpdatedNanos)
		return entry.machine, fmt.Errorf("stale entry for machine %q. Last updated %v", machineName, lastUpdated)
	}

	return entry.machine, nil
}

// ForceGet retrieves the specified AKS machine from the API and updates the cache.
func (c *MachineCache) ForceGet(ctx context.Context, aksMachineName string) (*armcontainerservice.Machine, error) {
	resp, err := c.client.Get(ctx, c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, aksMachineName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q: %w", aksMachineName, err)
	}
	aksMachine := lo.ToPtr(resp.Machine)

	entry := &cacheEntry{
		machine:           aksMachine,
		lastUpdatedUnixNs: atomic.Int64{},
	}
	entry.lastUpdatedUnixNs.Store(time.Now().UnixNano())
	c.machines.Store(aksMachineName, entry)

	return aksMachine, nil
}

// List returns all machines in the cache.
// Returns an error if the cache is not fresh and requests an update.
func (c *MachineCache) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	// Create a slice with all machines
	machines := make([]*armcontainerservice.Machine, 0)
	c.machines.Range(func(key, value any) bool {
		entry := value.(*cacheEntry)
		machines = append(machines, entry.machine)
		return true // continue iteration
	})

	return machines, nil
}

func (c *MachineCache) updateWorker() {
	defer c.wg.Done()

	ticker := time.NewTicker(activeRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.workerCtx.Done():
			return
		case <-ticker.C:
			if err := c.updateCache(c.workerCtx); err != nil {
				log.FromContext(c.workerCtx).Error(err, "failed to update machine cache")
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
	log.FromContext(ctx).Info("starting cache poller for AKS machine", "aksMachineName", name, "interval", c.pollInterval.String())
	c.activePollers.Add(1)
	defer c.activePollers.Add(-1)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	var retryAttemptsLeft int
	var currentRetryDelay time.Duration
	c.resetRetryState(&retryAttemptsLeft, &currentRetryDelay)

	if machine, err := c.ForceGet(ctx, name); err == nil {
		entry := &cacheEntry{
			machine:           machine,
			lastUpdatedUnixNs: atomic.Int64{},
		}
		entry.lastUpdatedUnixNs.Store(time.Now().UnixNano())
		c.machines.Store(name, entry)
	} else {
		log.FromContext(ctx).Error(err, "failed to force get AKS machine for cache poller", "aksMachineName", name)
	}

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
	machine, err := c.Get(aksMachineName)
	if err != nil {
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

func (c *MachineCache) updateCache(ctx context.Context) error {
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
	updateTime := time.Now()

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
			if isValid(ctx, aksMachine) && aksMachine.Name != nil {
				entry := &cacheEntry{
					machine: aksMachine,
				}
				entry.lastUpdatedUnixNs.Store(updateTime.UnixNano())
				c.machines.Store(*aksMachine.Name, entry)
				fetchedMachineNames[*aksMachine.Name] = struct{}{}
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
