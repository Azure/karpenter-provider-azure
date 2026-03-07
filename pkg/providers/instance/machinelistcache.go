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

package instance

import (
	"context"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// DefaultMachineListCacheTTL is the default time-to-live for machine list cache entries.
	// GET Machine 429s at 1K-node scale cost 17-29 seconds each; caching LIST results
	// converts O(N) individual GETs into O(1) cached lookups. A 30-second TTL is
	// acceptable because drift and reconciliation checks re-run on subsequent cycles anyway.
	DefaultMachineListCacheTTL = 30 * time.Second
)

// AKSMachineLister defines the interface for listing AKS machines.
type AKSMachineLister interface {
	List(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
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
type machineListCache struct {
	mu          sync.RWMutex
	machines    map[string]*armcontainerservice.Machine // keyed by machine name (not full ARM ID)
	lastUpdated time.Time
	ttl         time.Duration
	interval    time.Duration
	client      AKSMachineLister
}

func newMachineListCache(ttl time.Duration, client AKSMachineLister, interval time.Duration) *machineListCache {
	return &machineListCache{
		machines: make(map[string]*armcontainerservice.Machine),
		ttl:      ttl,
		interval: interval,
		client:   client,
	}
}

// isFresh returns true if the cache has been populated and hasn't expired.
func (c *machineListCache) isFresh() bool {
	return !c.lastUpdated.IsZero() && time.Since(c.lastUpdated) < c.ttl
}

// get retrieves a machine from the cache by name.
// Returns (machine, true) on cache hit, (nil, false) on miss or stale cache.
func (c *machineListCache) get(machineName string) (*armcontainerservice.Machine, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isFresh() {
		return nil, false
	}

	machine, ok := c.machines[machineName]
	return machine, ok
}

// update replaces the entire cache with the results of a LIST call.
func (c *machineListCache) update(machines []*armcontainerservice.Machine) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newCache := make(map[string]*armcontainerservice.Machine, len(machines))
	for _, m := range machines {
		if m.Name != nil {
			newCache[*m.Name] = m
		}
	}
	c.machines = newCache
	c.lastUpdated = time.Now()
}

// invalidate removes a specific machine from the cache, forcing the next Get()
// for that machine to fall through to the API. This is called after mutating
// operations (Create, Update, Delete) to prevent serving stale data.
func (c *machineListCache) invalidate(machineName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.machines, machineName)
}

// invalidateAll clears the entire cache, forcing all subsequent Get() calls
// to fall through to the API until the next List() repopulates the cache.
func (c *machineListCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.machines = make(map[string]*armcontainerservice.Machine)
	c.lastUpdated = time.Time{} // zero value → isFresh() returns false
}

func (c *machineListCache) poll(ctx context.Context, name string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:

			if !c.isFresh() {
				machines, err := c.client.List(ctx, "", "", "", name, nil)
				if err != nil {
					log.FromContext(ctx).Error(err, "failed to refresh AKS machine cache", "aksMachineName", name)
				} else {
					c.update(machines)
				}
			}

			machine, ok := c.get(name)
			if !ok {
				log.FromContext(ctx).Info("cache hit for AKS machine", "aksMachineName", name)
				return nil
			}

			// check if machine is in terminal state; if so, we can stop polling and rely on cache until next refresh
			if machine.Properties != nil && machine.Properties.ProvisioningState != nil {
				state := *machine.Properties.ProvisioningState
				log.FromContext(ctx).Info("polled AKS machine provisioning state", "aksMachineName", name, "state", state)
				if state == "Succeeded" || state == "Failed" || state == "Canceled" {
					log.FromContext(ctx).Info("AKS machine is in terminal provisioning state, stopping poller", "aksMachineName", name, "state", state)
					return nil
				}
			}

		case <-ctx.Done():
			log.FromContext(ctx).Info("stopping cache polling for AKS machine", "aksMachineName", name)
			return nil
		}
	}

}
