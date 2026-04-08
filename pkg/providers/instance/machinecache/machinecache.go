// Package machinecache provides an in-memory cache for AKS Machine API resources with TTL-based expiration and background refresh.
package machinecache

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	// DefaultMachineListCacheTTL is the default TTL for cached machine entries.
	DefaultMachineListCacheTTL = 30 * time.Second
	// DefaultMachineListCacheInterval is the default interval for periodic cache refresh.
	DefaultMachineListCacheInterval = 5 * time.Minute

	provisioningStateCreating  = "Creating"
	provisioningStateUpdating  = "Updating"
	provisioningStateDeleting  = "Deleting"
	provisioningStateSucceeded = "Succeeded"
	provisioningStateFailed    = "Failed"
)

var (
	// ErrCacheStale indicates the cache has exceeded its TTL and needs refresh.
	ErrCacheStale = errors.New("cache is stale")
	// ErrCacheMiss indicates the requested machine was not found in the cache.
	ErrCacheMiss = errors.New("machine not found in cache")
)

var (
	nodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

// AKSMachineNewListPager provides paginated list operations for AKS machines.
type AKSMachineNewListPager interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
}

// MachineCache caches AKS machine resources with TTL-based expiration and automatic background refresh.
type MachineCache struct {
	machines             sync.Map
	lastUpdatedUnixNanos atomic.Int64
	ttl                  time.Duration
	interval             time.Duration
	client               AKSMachineNewListPager

	clusterResourceGroup string
	clusterName          string
	aksMachinesPoolName  string

	maxRetries    int
	retryDelay    time.Duration
	maxRetryDelay time.Duration

	updateRequests chan struct{}
	workerCtx      context.Context
	workerCancel   context.CancelFunc
	updateInterval time.Duration
	wg             sync.WaitGroup
}

// NewMachineListCache creates a new cache instance with a background worker for automatic refresh.
func NewMachineListCache(ctx context.Context, ttl time.Duration, client AKSMachineNewListPager, interval time.Duration, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineCache {
	workerCtx, workerCancel := context.WithCancel(ctx)

	cache := &MachineCache{
		ttl:                  ttl,
		interval:             interval,
		client:               client,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
		maxRetries:           500,
		retryDelay:           1 * time.Second,
		maxRetryDelay:        30 * time.Second,
		updateRequests:       make(chan struct{}, 1),
		workerCtx:            workerCtx,
		workerCancel:         workerCancel,
		updateInterval:       5 * time.Minute,
	}

	cache.wg.Add(1)
	go cache.updateWorker()

	return cache
}

// Get retrieves a machine from the cache by name if the cache is fresh.
func (c *MachineCache) Get(machineName string) (*armcontainerservice.Machine, error) {
	if !c.isFresh() {
		c.requestUpdate()
		return nil, fmt.Errorf("%w for machine %q", ErrCacheStale, machineName)
	}

	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, fmt.Errorf("%w: machine %q", ErrCacheMiss, machineName)
	}

	return value.(*armcontainerservice.Machine), nil
}

// List returns all cached machines, blocking until the cache is fresh.
func (c *MachineCache) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	if !c.isFresh() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if !c.isFresh() {
					c.requestUpdate()
				}
			case <-ctx.Done():
				return nil, fmt.Errorf("context canceled while waiting for fresh cache: %w", ctx.Err())
			}

			if c.isFresh() {
				break
			}
		}
	}

	machines := make([]*armcontainerservice.Machine, 0)
	c.machines.Range(func(key, value any) bool {
		machines = append(machines, value.(*armcontainerservice.Machine))
		return true
	})

	return machines, nil
}

// Shutdown stops the background worker and waits for it to finish.
func (c *MachineCache) Shutdown() {
	c.workerCancel()
	c.wg.Wait()
}

// Invalidate removes a specific machine from the cache by name.
func (c *MachineCache) Invalidate(machineName string) {
	c.machines.Delete(machineName)
}

// PollUntilDone polls for AKS machine provisioning completion using the cache.
func (c *MachineCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
	log.FromContext(ctx).Info("starting cache poller for AKS machine", "aksMachineName")
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

func (c *MachineCache) pollOnce(ctx context.Context, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	machine, err := c.Get(aksMachineName)
	if err != nil {
		if errors.Is(err, ErrCacheStale) || errors.Is(err, ErrCacheMiss) {
			c.requestUpdate()
			log.FromContext(ctx).Info("cache miss for AKS machine during poll, requested update",
				"aksMachineName", aksMachineName,
				"retryAttemptsLeft", *retryAttemptsLeft,
			)
		}

		shouldRetry, backoffErr := c.retryWithBackoff(ctx, retryAttemptsLeft, currentRetryDelay)
		if backoffErr != nil {
			return nil, backoffErr, true
		}
		if shouldRetry {
			return nil, nil, false
		}
		return nil, fmt.Errorf("cache poller: exhausted retries due to repeated cache misses for AKS machine %q", aksMachineName), true
	}

	c.resetRetryState(retryAttemptsLeft, currentRetryDelay)

	if machine.Properties == nil || machine.Properties.ProvisioningState == nil {
		return c.handleNilProvisioningState(ctx, machine, aksMachineName, retryAttemptsLeft, currentRetryDelay)
	}

	return c.handleProvisioningState(ctx, machine, aksMachineName, retryAttemptsLeft, currentRetryDelay)
}

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

func (c *MachineCache) handleProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	provisioningState := *aksMachine.Properties.ProvisioningState
	switch provisioningState {
	case consts.ProvisioningStateCreating, consts.ProvisioningStateUpdating:
		log.FromContext(ctx).V(2).Info("Cache poller: polling for AKS machine ongoing",
			"aksMachineName", aksMachineName,
			"aksMachineID", aksMachine.ID,
			"provisioningState", provisioningState,
		)
		c.resetRetryState(retryAttemptsLeft, currentRetryDelay)
		return nil, nil, false

	case consts.ProvisioningStateDeleting:
		return nil, fmt.Errorf("AKS machine %q sees canceled provisioning state %s", aksMachineName, provisioningState), true

	case consts.ProvisioningStateSucceeded:
		return nil, nil, true

	case consts.ProvisioningStateFailed:
		if aksMachine.Properties.Status != nil && aksMachine.Properties.Status.ProvisioningError != nil {
			return aksMachine.Properties.Status.ProvisioningError, nil, true
		}
		return nil, fmt.Errorf("AKS machine %q sees fatal provisioning state %s, but ProvisioningError is nil", aksMachineName, provisioningState), true

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

func (c *MachineCache) updateWorker() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.workerCtx.Done():
			return

		case <-c.updateRequests:
			if err := c.update(c.workerCtx); err != nil {
				log.FromContext(c.workerCtx).Error(err, "background cache update failed (on-demand)")
			}

		case <-ticker.C:
			if err := c.update(c.workerCtx); err != nil {
				log.FromContext(c.workerCtx).Error(err, "background cache update failed (periodic)")
			}
		}
	}
}

func (c *MachineCache) update(ctx context.Context) error {
	if c.isFresh() {
		return nil
	}
	log.FromContext(ctx).Info("Start update for machine cache", "aksMachinesPoolName", c.aksMachinesPoolName)

	pager := c.client.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return fmt.Errorf("failed to list AKS machines: created pager is nil")
	}

	fetchedMachineNames := make(map[string]struct{})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAKSMachineOrMachinesPoolNotFound(err) {
				log.FromContext(ctx).V(1).Info("failed to list AKS machines: AKS machines pool not found, treating as no AKS machines found")
				break
			}

			return fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			if isValid(ctx, aksMachine.Properties, lo.FromPtr(aksMachine.Name)) {
				c.machines.Store(lo.FromPtr(aksMachine.Name), aksMachine)
				fetchedMachineNames[lo.FromPtr(aksMachine.Name)] = struct{}{}
			}
		}

	}

	c.machines.Range(func(key, value any) bool {
		machineName := key.(string)
		if _, exists := fetchedMachineNames[machineName]; !exists {
			c.machines.Delete(machineName)
		}
		return true
	})

	log.FromContext(ctx).Info("machine list cache updated", "total", len(fetchedMachineNames))
	c.lastUpdatedUnixNanos.Store(time.Now().UnixNano())
	return nil
}

func (c *MachineCache) isFresh() bool {
	lastUpdatedNanos := c.lastUpdatedUnixNanos.Load()
	if lastUpdatedNanos == 0 {
		return false
	}
	lastUpdated := time.Unix(0, lastUpdatedNanos)
	return time.Since(lastUpdated) < c.ttl
}

func (c *MachineCache) requestUpdate() {
	select {
	case c.updateRequests <- struct{}{}:
	default:
	}
}

func isAKSMachineOrMachinesPoolNotFound(err error) bool {
	if err == nil {
		return false
	}
	azErr := sdkerrors.IsResponseError(err)
	if azErr != nil && (azErr.StatusCode == http.StatusNotFound ||
		(azErr.StatusCode == http.StatusBadRequest && azErr.ErrorCode == "InvalidParameter" && strings.Contains(azErr.Error(), "Cannot find any valid machines"))) {
		return true
	}
	return false
}

func isValid(ctx context.Context, properties *armcontainerservice.MachineProperties, machineName string) bool {
	if properties == nil || properties.Tags == nil {
		log.FromContext(ctx).Info("skipping AKS machine with nil properties or tags", "aksMachineName", machineName)
		return false
	}
	if _, hasTags := properties.Tags[nodePoolTagKey]; !hasTags {
		log.FromContext(ctx).Info("skipping AKS machine without Karpenter nodepool tag", "aksMachineName", machineName)
		return false
	}

	return true
}
