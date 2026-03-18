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

var (
	nodePoolTagKey = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
)

type AKSMachineNewListPager interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
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
	mu                   sync.RWMutex
	machines             map[string]*armcontainerservice.Machine // keyed by machine name (not full ARM ID)
	lastUpdatedUnixNanos atomic.Int64                            // nanoseconds since epoch; 0 means never updated
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

func NewMachineListCache(ctx context.Context, ttl time.Duration, client AKSMachineNewListPager, interval time.Duration, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineListCache {
	workerCtx, workerCancel := context.WithCancel(ctx)

	cache := &MachineListCache{
		machines:             make(map[string]*armcontainerservice.Machine),
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
func (c *MachineListCache) isFresh() bool {
	lastUpdatedNanos := c.lastUpdatedUnixNanos.Load()
	if lastUpdatedNanos == 0 {
		return false
	}
	lastUpdated := time.Unix(0, lastUpdatedNanos)
	return time.Since(lastUpdated) < c.ttl
}

// Get retrieves a machine from the cache by name.
// Returns the machine if cache is fresh and contains the machine.
// Returns an error if the cache is stale or the machine is not found.
func (c *MachineListCache) Get(machineName string) (*armcontainerservice.Machine, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isFresh() {
		c.RequestUpdate()
		return nil, fmt.Errorf("cache is stale for machine %q", machineName)
	}

	machine, ok := c.machines[machineName]
	if !ok {
		return nil, fmt.Errorf("machine %q not found in cache", machineName)
	}

	return machine, nil
}

// List returns all machines in the cache.
// Returns an error if the cache is not fresh and requests an update.
func (c *MachineListCache) List() ([]*armcontainerservice.Machine, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isFresh() {
		c.RequestUpdate()
		return nil, fmt.Errorf("cache is not fresh")
	}

	// Create a slice with all machines
	machines := make([]*armcontainerservice.Machine, 0, len(c.machines))
	for _, machine := range c.machines {
		machines = append(machines, machine)
	}

	return machines, nil
}

// get retrieves a machine from the cache by name.
func (c *MachineListCache) get(machineName string) (*armcontainerservice.Machine, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	machine, ok := c.machines[machineName]
	return machine, ok
}

// invalidate removes a specific machine from the cache, forcing the next Get()
// for that machine to fall through to the API. This is called after mutating
// operations (Create, Update, Delete) to prevent serving stale data.
func (c *MachineListCache) invalidate(machineName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.machines, machineName)
}

// invalidateAll clears the entire cache, forcing all subsequent Get() calls
// to fall through to the API until the next List() repopulates the cache.
func (c *MachineListCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.machines = make(map[string]*armcontainerservice.Machine)
	c.lastUpdatedUnixNanos.Store(0) // zero value → isFresh() returns false
}

// updateWorker runs in a background goroutine and handles both periodic and on-demand cache updates.
// It stops when workerCtx is canceled, ensuring clean shutdown via Shutdown().
func (c *MachineListCache) updateWorker() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.workerCtx.Done():
			// Shutdown signal received
			return

		case <-c.updateRequests:
			// On-demand update requested via RequestUpdate()
			if err := c.update(c.workerCtx); err != nil {
				// Log error but continue - periodic refresh will retry
				log.FromContext(c.workerCtx).Error(err, "background cache update failed (on-demand)")
			}

		case <-ticker.C:
			// Periodic refresh to keep cache fresh
			if err := c.update(c.workerCtx); err != nil {
				// Log error but continue - next tick will retry
				log.FromContext(c.workerCtx).Error(err, "background cache update failed (periodic)")
			}
		}
	}
}

// RequestUpdate sends a non-blocking update request to the background worker.
// If an update is already pending (channel buffer full), this is a no-op.
// This method never blocks the caller - use it to hint that a cache refresh would be beneficial.
func (c *MachineListCache) RequestUpdate() {
	select {
	case c.updateRequests <- struct{}{}:
		// Update request successfully enqueued
	default:
		// Channel buffer full - update already pending, do nothing
	}
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
	// Request cache refresh if stale (background worker will handle it)
	if !c.isFresh() {
		c.RequestUpdate()
		return nil, nil, false
	}

	// Get machine from cache (may be stale, but worker is updating)
	machine, ok := c.get(aksMachineName)
	if !ok {
		// Cache miss - request update and handle as transient error
		c.RequestUpdate()
		log.FromContext(ctx).Info("cache miss for AKS machine during poll, requested update",
			"aksMachineName", aksMachineName,
			"retryAttemptsLeft", *retryAttemptsLeft,
		)
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

func (c *MachineListCache) update(ctx context.Context) error {
	// Check freshness without lock (atomic operation)
	if c.isFresh() {
		return nil
	}

	now := time.Now()
	defer func() {
		log.FromContext(ctx).Info("finished updating machine list cache",
			"duration", time.Since(now).String(),
			"aksMachinesPoolName", c.aksMachinesPoolName,
		)
	}()

	// Perform LIST API call WITHOUT holding lock (can take 17-29 seconds)
	pager := c.client.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return fmt.Errorf("failed to list AKS machines: created pager is nil")
	}

	// Build new map outside of lock
	newMachines := make(map[string]*armcontainerservice.Machine)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAKSMachineOrMachinesPoolNotFound(err) {
				// AKS machines pool not found. Handle gracefully.
				// Suggestion: separate the util function to not cover more than needed?
				log.FromContext(ctx).V(1).Info("failed to list AKS machines: AKS machines pool not found, treating as no AKS machines found")
				break
			}

			return fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			// Filter to only include machines created by Karpenter
			// Check if the AKS machine has the Karpenter nodepool tag
			if aksMachine.Properties != nil && aksMachine.Properties.Tags != nil {
				if _, hasKarpenterTag := aksMachine.Properties.Tags[nodePoolTagKey]; hasKarpenterTag {
					if aksMachine.Name != nil {
						newMachines[*aksMachine.Name] = aksMachine
					}
				} else {
					log.FromContext(ctx).V(1).Info("skipping AKS machine without Karpenter nodepool tag",
						"aksMachineName", lo.FromPtr(aksMachine.Name),
					)
				}
			} else {
				log.FromContext(ctx).V(1).Info("skipping AKS machine with nil tags",
					"aksMachineName", lo.FromPtr(aksMachine.Name),
				)
			}
		}
	}

	fmt.Printf("Machine list cache updated with %d machines\n", len(newMachines))

	// ONLY lock when swapping the map (fast operation - microseconds)
	c.mu.Lock()
	c.machines = newMachines
	c.mu.Unlock()

	c.lastUpdatedUnixNanos.Store(time.Now().UnixNano())
	return nil
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
