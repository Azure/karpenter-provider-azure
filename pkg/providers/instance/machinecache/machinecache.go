// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package machinecache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/machine"

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AKSMachineClienter provides operations for AKS machines.
type AKSMachineClienter interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, machineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
}

type opts struct {
	ttl          time.Duration
	pollInterval time.Duration
	pollTimeout  time.Duration
}

func defaultOpts() opts {
	return opts{
		// ttl is the duration for which a cached machine is considered fresh before it is considered stale.
		// It defaults to 30 seconds, which is consistent with the max retry delay of the original GET poller.
		ttl: 30 * time.Second,
		// pollInterval is the duration between successive polls when waiting for a machine to reach a terminal provisioning state.
		// It defaults to 5 seconds, which is consistent with the polling interval used by the original GET poller.
		pollInterval: 5 * time.Second,
		// pollTimeout is the maximum duration to wait for a machine to reach a terminal provisioning state
		// before considering the poll to have timed out. It defaults to 15 minutes, which is the maximum
		// time a NodeClaim has to register in Karpenter core before it is considered failed and deleted.
		pollTimeout: 15 * time.Minute,
	}
}

// Option is a functional option for configuring MachineCache.
type Option func(opts) opts

// WithTTL sets a custom Time-to-Live (TTL) for the cache. It determines how long the cache is considered fresh before it needs to be refreshed. A TTL of 0 means the cache is always stale.
func WithTTL(d time.Duration) Option {
	return func(o opts) opts { o.ttl = d; return o }
}

// WithPollInterval sets the interval for polling machine provisioning state.
func WithPollInterval(d time.Duration) Option {
	return func(o opts) opts { o.pollInterval = d; return o }
}

// WithPollTimeout sets the maximum duration to wait for a machine to reach a terminal provisioning state
// before considering the poll to have timed out.
func WithPollTimeout(d time.Duration) Option {
	return func(o opts) opts { o.pollTimeout = d; return o }
}

// MachineCache caches AKS machine resources with TTL-based expiration.
type MachineCache struct {
	machines             sync.Map
	lastUpdatedUnixNanos atomic.Int64
	client               AKSMachineClienter

	clusterResourceGroup string
	clusterName          string
	aksMachinesPoolName  string

	updateRequests chan struct{}
	wg             sync.WaitGroup

	options opts
}

// NewMachineCache creates a new cache instance with a background worker for updates.
// Updates to the cache are triggered when a stale cache is accessed.
func NewMachineCache(ctx context.Context, client AKSMachineClienter, clusterResourceGroup, clusterName, aksMachinesPoolName string, opts ...Option) *MachineCache {
	cache := &MachineCache{
		client:               client,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
		updateRequests:       make(chan struct{}, 1),
		options:              defaultOpts(),
	}

	for _, opt := range opts {
		cache.options = opt(cache.options)
	}

	cache.wg.Add(1)
	go cache.run(ctx)

	return cache
}

// GetWithFallback gets a machine.
// If useCache is true and the cache is fresh, it will attempt to return the machine from the cache.
// If the cache is stale or disabled, or if the machine is not found in the cache, it will fall back to calling the AKS API directly.
func (c *MachineCache) GetWithFallback(ctx context.Context, machineName string, useCache bool) (*armcontainerservice.Machine, error) {
	if useCache {
		machine, found, fresh := c.getFromCache(machineName)
		if fresh && found {
			c.rehydrateMachine(machine)
			return machine, nil
		}

		// Even if the cache is fresh but the machine is not found, we fall through to call the AKS API directly.
		// This ensures that an out-of-date but fresh cache does not prevent us from retrieving a machine.
		// It also ensures that the returned error for a missing machine is consistent.
		// Multiple functions in this package also rely on the assumption that we have a fallback to the API
		// in cases where the cache is stale or the machine is not found in the cache.
	}

	// In terms of performance, calling the AKS API directly here is tolerable.
	// The bulk of Get calls come from polling, and it's rare for a call to fall through to direct API calls during polling.
	resp, err := c.client.Get(ctx, c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, machineName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get AKS machine %q: %w", machineName, err)
	}

	machine := lo.ToPtr(resp.Machine)
	c.rehydrateMachine(machine)
	c.machines.Store(machineName, machine)

	return machine, nil
}

// getFromCache retrieves a machine from the cache by name.
// It returns the machine, a boolean indicating if it was found, and a boolean indicating if the cache is fresh.
func (c *MachineCache) getFromCache(machineName string) (*armcontainerservice.Machine, bool, bool) {
	if !c.isFresh() {
		// Note: We do not block waiting for the background cache update to complete because doing so would introduce substantial latency.
		// Performance-wise, it's preferable to tolerate a few Gets than to block provisioning until the cache populates.
		c.requestUpdate()
		return nil, false, false
	}
	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, false, true
	}

	return value.(*armcontainerservice.Machine), true, true
}

// ListWithFallback lists all machines in the AKS machines pool.
// If useCache is true and the cache is fresh, it will attempt to return the list from the cache.
// If the cache is stale or disabled, it will fall back to calling the AKS API directly.
func (c *MachineCache) ListWithFallback(ctx context.Context, useCache bool) ([]*armcontainerservice.Machine, error) {
	if useCache {
		if machines, fresh := c.listFromCache(); fresh {
			return machines, nil
		}
	}

	// We fall back to calling the AKS API directly when the cache is stale or disabled.
	// There will be duplicate List calls from time to time, but List calls are infrequent enough
	// that the performance impact is acceptable.
	var machines []*armcontainerservice.Machine
	pager := c.client.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return nil, fmt.Errorf("failed to list AKS machines: created pager is nil")
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if machine.IsAKSMachineOrMachinesPoolNotFound(err) {
				log.FromContext(ctx).V(1).Info("failed to list AKS machines: AKS machines pool not found, treating as no AKS machines found")
				break
			}

			return nil, fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			if isValid(aksMachine.Properties) {
				c.rehydrateMachine(aksMachine)
				machines = append(machines, aksMachine)
			}
		}
	}
	return machines, nil
}

// listFromCache returns the list of machines from the cache if the cache is fresh and a boolean indicating whether the cache was fresh.
func (c *MachineCache) listFromCache() ([]*armcontainerservice.Machine, bool) {
	if !c.isFresh() {
		c.requestUpdate()
		return nil, false
	}
	var machines []*armcontainerservice.Machine
	c.machines.Range(func(key, value any) bool {
		if m, ok := value.(*armcontainerservice.Machine); ok {
			if isValid(m.Properties) {
				c.rehydrateMachine(m)
				machines = append(machines, m)
			}
		}
		return true
	})
	return machines, true
}

// Invalidate removes a specific machine from the cache by name.
func (c *MachineCache) Invalidate(machineName string) {
	// We remove invalidated machines from the cache.
	// This is safe because any subsequent GetWithFallback call for this machine will fall back to an API call.
	c.machines.Delete(machineName)
}

// InvalidateAll clears the entire cache, forcing the next access to fall through to the API.
func (c *MachineCache) InvalidateAll() {
	c.machines.Range(func(key, _ any) bool {
		c.machines.Delete(key)
		return true
	})
	c.lastUpdatedUnixNanos.Store(0)
}

// PollUntilDone polls for AKS machine provisioning completion using the cache.
// This polls indefinitely until the machine reaches a terminal state (Succeeded, Failed, or Deleting) or the context is canceled.
// If at any point the machine is not found, PollUntilDone will return an error.
func (c *MachineCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
	log.FromContext(ctx).V(2).Info("starting cache poller for AKS machine", "aksMachineName", name)

	if !c.checkMachineExists(ctx, name) {
		return nil, fmt.Errorf("AKS machine %q does not exist", name)
	}

	ticker := time.NewTicker(c.options.pollInterval)
	defer ticker.Stop()

	timeout := time.After(c.options.pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while polling for AKS machine %q: %w", name, ctx.Err())

		case <-ticker.C:
			provisioningErr, pollerErr, done := c.pollOnce(ctx, name)
			if done {
				return provisioningErr, pollerErr
			}
		case <-timeout:
			return nil, fmt.Errorf("timed out while polling for AKS machine %q after %s", name, c.options.pollTimeout)
		}
	}
}

func (c *MachineCache) checkMachineExists(ctx context.Context, name string) bool {
	machine, err := c.GetWithFallback(ctx, name, true)
	return machine != nil && err == nil
}

func (c *MachineCache) pollOnce(ctx context.Context, aksMachineName string) (*armcontainerservice.ErrorDetail, error, bool) {
	aksMachine, found, fresh := c.getFromCache(aksMachineName)
	if !fresh {
		return nil, nil, false
	}

	// Terminate early. This indicates the machine was not found in the cache and there is no point in continuing to poll
	if !found || aksMachine == nil {
		// Double check the cache to ensure the machine truly does not exist before returning a terminal error
		machine, err := c.GetWithFallback(ctx, aksMachineName, true)
		if machine != nil && err == nil {
			return nil, nil, false
		}
		return nil, fmt.Errorf("AKS machine %q not found in cache", aksMachineName), true
	}

	if aksMachine.Properties == nil || aksMachine.Properties.ProvisioningState == nil {
		log.FromContext(ctx).V(1).Info("Cache poller: warning: polling for AKS machine found nil provisioning state, will retry",
			"aksMachineName", aksMachineName,
			"aksMachineID", aksMachine.ID,
		)
		return nil, nil, false
	}

	return machine.HandleProvisioningState(ctx, aksMachine)
}

func (c *MachineCache) run(ctx context.Context) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case <-c.updateRequests:
			if err := c.update(ctx); err != nil {
				log.FromContext(ctx).Error(err, "cache update failed")
			}
		}
	}
}

// update refreshes the machine cache by fetching the latest list of AKS machines from the Azure API.
// This should NOT be called directly; it is intended to be used by the background worker.
func (c *MachineCache) update(ctx context.Context) error {
	if c.isFresh() {
		return nil
	}

	pager := c.client.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return fmt.Errorf("failed to list AKS machines: created pager is nil")
	}

	fetchedMachineNames := make(map[string]struct{})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if machine.IsAKSMachineOrMachinesPoolNotFound(err) {
				log.FromContext(ctx).V(1).Info("failed to list AKS machines: AKS machines pool not found, treating as no AKS machines found")
				break
			}
			return fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			c.machines.Store(lo.FromPtr(aksMachine.Name), aksMachine)
			fetchedMachineNames[lo.FromPtr(aksMachine.Name)] = struct{}{}
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
	return time.Since(lastUpdated) < c.options.ttl
}

func (c *MachineCache) requestUpdate() {
	select {
	case c.updateRequests <- struct{}{}:
	default:
	}
}

func isValid(properties *armcontainerservice.MachineProperties) bool {
	if properties == nil || properties.Tags == nil {
		return false
	}
	if _, hasTags := properties.Tags[launchtemplate.NodePoolTagKey]; !hasTags {
		return false
	}

	return true
}

func (c *MachineCache) rehydrateMachine(aksMachine *armcontainerservice.Machine) {
	// This needs to be rehydrated per the current behavior of both AKS machine API and AKS AgentPool API: priority will shows up only for spot.
	// An example use of this down the codepath is  to construct a NodeClaim representation (BuildNodeClaimFromAKSMachine).
	// Suggestion: rework/research more on this pattern RP-side?
	if aksMachine.Properties != nil && aksMachine.Properties.Priority == nil {
		aksMachine.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
	}
}
