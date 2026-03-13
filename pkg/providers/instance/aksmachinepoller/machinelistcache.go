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
}

func NewMachineListCache(ttl time.Duration, client AKSMachineNewListPager, interval time.Duration, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineListCache {
	return &MachineListCache{
		machines:             make(map[string]*armcontainerservice.Machine),
		ttl:                  ttl,
		interval:             interval,
		client:               client,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
	}
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

// get retrieves a machine from the cache by name.
// Returns (machine, true) on cache hit, (nil, false) on miss or stale cache.
func (c *MachineListCache) get(machineName string) (*armcontainerservice.Machine, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isFresh() {
		return nil, false
	}

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

func (c *MachineListCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
	fmt.Printf("Starting cache poller for AKS machine %q with interval %s\n", name, c.interval)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	retries := 10

	for {
		select {
		case <-ticker.C:

			if !c.isFresh() {

				if err := c.update(ctx); err != nil {
					log.FromContext(ctx).Error(err, "failed to update machine list cache")

					if retries > 0 {
						retries--
						log.FromContext(ctx).Info("retrying poll", "remainingRetries", retries)
						continue
					} else {
						log.FromContext(ctx).Error(nil, "exhausted retries for cache update, stopping cache poller",
							"aksMachineName", name,
						)
						return nil, fmt.Errorf("cache poller: failed to update machine list cache after multiple attempts: %w", err)
					}
				}

				log.FromContext(ctx).Info("cache updated successfully during poll")
				retries = 10 // reset retries after a successful update
			}

			machine, ok := c.get(name)
			if !ok {
				log.FromContext(ctx).Info("cache miss for AKS machine during poll", "aksMachineName", name)
				continue
			}

			if machine.Properties == nil || machine.Properties.ProvisioningState == nil {
				log.FromContext(ctx).Info("cache poller found AKS machine with nil provisioning state,", "aksMachineName", name)
				continue
			}

			state := *machine.Properties.ProvisioningState
			log.FromContext(ctx).Info("polled AKS machine provisioning state", "aksMachineName", name, "state", state)

			done, err := c.handleState(ctx, state, machine)
			if err != nil {
				return machine.Properties.Status.ProvisioningError, err
			}
			if !done {
				continue
			}

			return nil, nil

		case <-ctx.Done():
			log.FromContext(ctx).Info("stopping cache polling for AKS machine", "aksMachineName", name)
			return nil, nil
		}
	}

}

func (c *MachineListCache) update(ctx context.Context) error {
	// Update the cache with fresh data
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check freshness after acquiring lock to avoid redundant updates
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

	pager := c.client.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return fmt.Errorf("failed to list AKS machines: created pager is nil")
	}

	// Clear existing cache
	c.machines = make(map[string]*armcontainerservice.Machine)

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
						c.machines[*aksMachine.Name] = aksMachine
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

	fmt.Printf("Machine list cache updated with %d machines\n", len(c.machines))

	c.lastUpdatedUnixNanos.Store(time.Now().UnixNano())
	return nil
}

func (c *MachineListCache) handleState(ctx context.Context, state string, machine *armcontainerservice.Machine) (done bool, err error) {
	machineName := lo.FromPtr(machine.Name)

	// Check for provisioning error regardless of state
	if machine.Properties.Status != nil && machine.Properties.Status.ProvisioningError != nil {
		log.FromContext(ctx).Error(nil, "Cache poller: AKS machine provisioning error details",
			"aksMachineName", machineName,
			"provisioningError", machine.Properties.Status.ProvisioningError,
		)
		return true, fmt.Errorf("AKS machine %s has provisioning error", machineName)
	}

	switch state {
	case ProvisioningStateFailed:
		log.FromContext(ctx).Info("Cache poller: AKS machine provisioning failed",
			"aksMachineName", machineName,
			"provisioningState", state,
		)
		return true, fmt.Errorf("AKS machine %s provisioning failed", machineName)

	case ProvisioningStateCreating, ProvisioningStateUpdating:
		log.FromContext(ctx).V(2).Info("Cache poller: polling for AKS machine ongoing",
			"aksMachineName", machineName,
			"provisioningState", state,
		)
		return false, nil // not done, keep polling

	case ProvisioningStateSucceeded:
		log.FromContext(ctx).Info("Cache poller: AKS machine provisioning succeeded",
			"aksMachineName", machineName,
			"provisioningState", state,
		)
		return true, nil // done, no error

	case ProvisioningStateDeleting:
		log.FromContext(ctx).Info("Cache poller: AKS machine is deleting",
			"aksMachineName", machineName,
			"provisioningState", state,
		)
		return true, fmt.Errorf("AKS machine %s is being deleted", machineName)

	default:
		log.FromContext(ctx).V(1).Info("Cache poller: unrecognized provisioning state, continuing to poll",
			"aksMachineName", machineName,
			"provisioningState", state,
		)
		return false, nil // continue polling
	}
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
