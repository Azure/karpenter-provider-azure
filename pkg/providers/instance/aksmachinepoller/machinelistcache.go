/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aksmachinepoller

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	// DefaultMachineListCacheTTL is the default time-to-live for machine list cache entries.
	// GET Machine 429s at 1K-node scale cost 17-29 seconds each; caching LIST results
	// converts O(N) individual GETs into O(1) cached lookups. A 30-second TTL is
	// acceptable because drift and reconciliation checks re-run on subsequent cycles anyway.
	DefaultMachineListCacheTTL = 30 * time.Second

	// Provisioning state constants for AKS Machine API
	ProvisioningStateCreating  = "Creating"
	ProvisioningStateUpdating  = "Updating"
	ProvisioningStateDeleting  = "Deleting"
	ProvisioningStateSucceeded = "Succeeded"
	ProvisioningStateFailed    = "Failed"
)

const numWorkers = 100

var (
	nodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

type AKSMachineNewListPager interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
}

type machineListItem struct {
	lastUpdatedTime atomic.Int64
	machine         *armcontainerservice.Machine
	err             error
}

// machineListCache caches the results of LIST Machine API calls, keyed by machine name.
// It reduces O(N) individual GET Machine calls (for drift checks, reconciliation, etc.)
// to O(1) cached lookups between LIST refreshes.
//
// Thread-safety: all methods are safe for concurrent use.
//
// Invalidation strategy:
//   - TTL-based: entries expire after cacheTTL (default 30s)
//   - Explicit: mutating operations (Create, Update, Delete) invalidate the affected entry
//   - Full refresh: List() replaces the entire cache
//
// Update strategy:
//   - Background worker goroutine handles all cache updates
//   - Periodic refresh every 5 minutes keeps cache fresh
//   - On-demand updates via RequestUpdate() channel (non-blocking)
type MachineListCache struct {
	machines sync.Map // keyed by machine name (not full ARM ID), value is *machineListItem
	ttl      time.Duration
	interval time.Duration
	client   AKSMachineNewListPager

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

	workerChan chan string
}

func NewMachineListCache(ctx context.Context, ttl time.Duration, client AKSMachineNewListPager, interval time.Duration, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineListCache {
	workerCtx, workerCancel := context.WithCancel(ctx)

	cache := &MachineListCache{
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
	go cache.refreshWorker()

	for range numWorkers {
		cache.wg.Add(1)
		go cache.worker()
	}

	return cache
}

// isFresh returns true if the cache has been populated and hasn't expired.
// Lock-free implementation using atomic operations for better concurrency.
func (c *MachineListCache) isFresh(machineName string) bool {
	value, ok := c.machines.Load(machineName)
	if !ok {
		return false
	}
	item := value.(*machineListItem)
	lastUpdatedNanos := item.lastUpdatedTime.Load()
	if lastUpdatedNanos == 0 {
		return false
	}
	lastUpdated := time.Unix(0, lastUpdatedNanos)
	return time.Since(lastUpdated) < c.ttl
}

// Get retrieves a machine from the cache by name.
// Returns the machine if cache is fresh and contains the machine.
// Returns an error if the cache is stale or the machine is not found.
func (c *MachineListCache) Get(ctx context.Context, machineName string) (*armcontainerservice.Machine, error) {
	return c.freshGet(ctx, machineName)
}

/*
// List returns all machines in the cache.
// Returns an error if the cache is not fresh and requests an update.
func (c *MachineListCache) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !c.isFresh() {
				c.RequestUpdate()
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while waiting for fresh cache: %w", ctx.Err())
		}

		if c.isFresh() {
			break
		}
	}

	// Create a slice with all machines
	machines := make([]*armcontainerservice.Machine, 0)
	c.machines.Range(func(key, value any) bool {
		machines = append(machines, value.(*machineListItem).machine)
		return true // continue iteration
	})

	return machines, nil
}
*/

func (c *MachineListCache) refreshWorker() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.workerCtx.Done():
			// Shutdown signal received
			return

		case <-ticker.C:
			// Periodic refresh to keep cache fresh
			c.machines.Range(func(k, v any) bool {
				c.workerChan <- k.(string)
				return true
			})
		}
	}
}

func (c *MachineListCache) worker() {
	defer c.wg.Done()

	for {
		select {
		case <-c.workerCtx.Done():
			return
		case m := <-c.workerChan:
			c.freshGet(c.workerCtx, m)
		}
	}
}

func (c *MachineListCache) freshGet(ctx context.Context, machineName string) (*armcontainerservice.Machine, error) {
	if !c.isFresh(machineName) {
		c.add(ctx, machineName)
	}

	item, err, ok := c.load(machineName)
	if !ok {
		return nil, fmt.Errorf("machine %q not found in cache", machineName)
	}
	return item, err
}

func (c *MachineListCache) add(ctx context.Context, machine string) {
	resp, getErr := c.client.Get(ctx, c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, machine, nil)
	if getErr != nil {
		log.FromContext(ctx).Error(getErr, "failed to get AKS machine", "aksMachineName", machine)
	}

	item := &machineListItem{
		machine: &resp.Machine,
		err:     getErr,
	}
	item.lastUpdatedTime.Store(time.Now().UnixNano())
	c.machines.Store(machine, item)
}

func (c *MachineListCache) load(machineName string) (*armcontainerservice.Machine, error, bool) {
	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, nil, false
	}
	item := value.(*machineListItem)
	return item.machine, item.err, true
}

// Shutdown stops the background update worker and waits for it to finish.
// Call this during provider shutdown to prevent goroutine leaks.
// After calling Shutdown, the cache will no longer receive automatic updates.
func (c *MachineListCache) Shutdown() {
	c.workerCancel() // Signal worker to stop
	c.wg.Wait()      // Wait for worker goroutine to finish
}

func (c *MachineListCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
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
func (c *MachineListCache) pollOnce(ctx context.Context, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	machine, err := c.freshGet(ctx, aksMachineName)
	if err != nil {
		return c.handleGetError(ctx, err, aksMachineName, retryAttemptsLeft, currentRetryDelay)
	}

	if machine.Properties == nil || machine.Properties.ProvisioningState == nil {
		return c.handleNilProvisioningState(ctx, machine, aksMachineName, retryAttemptsLeft, currentRetryDelay)
	}

	return c.handleProvisioningState(ctx, machine, aksMachineName, retryAttemptsLeft, currentRetryDelay)
}

// handleNilProvisioningState handles the case where the machine's provisioning state is nil.
func (c *MachineListCache) handleNilProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
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
func (c *MachineListCache) handleProvisioningState(ctx context.Context, aksMachine *armcontainerservice.Machine, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
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

func (c *MachineListCache) handleGetError(ctx context.Context, err error, aksMachineName string, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (*armcontainerservice.ErrorDetail, error, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("failed to get AKS machine %q during polling as context is canceled: %w", aksMachineName, err), true
	}

	if !isTransientError(err) {
		// Non-transient error (not found, auth, permissions, etc.) - fail immediately
		// Not found is possible if the AKS machine is deleted mid-way.
		// If the deletion takes time, it might appear with provisioning state "Deleting" before this can be reached.
		return nil, fmt.Errorf("failed to get AKS machine %q during polling with non-retryable error: %w", aksMachineName, err), true
	}

	log.FromContext(ctx).V(1).Info("Poller: polling for AKS machine failed to get AKS machine, may retry",
		"aksMachineName", aksMachineName,
		"error", err,
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
	return nil, fmt.Errorf("failed to get AKS machine %q during polling: %w after exhausting %d retry attempts", aksMachineName, err, c.maxRetries), true
}

// retryWithBackoff applies exponential backoff and returns true if retry should continue, false if exhausted.
func (c *MachineListCache) retryWithBackoff(ctx context.Context, retryAttemptsLeft *int, currentRetryDelay *time.Duration) (shouldRetry bool, err error) {
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

func (c *MachineListCache) resetRetryState(retryAttemptsLeft *int, currentRetryDelay *time.Duration) {
	*retryAttemptsLeft = c.maxRetries
	*currentRetryDelay = c.retryDelay
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
