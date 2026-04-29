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
	"errors"
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

var (
	// ErrCacheStale indicates the cache has exceeded its TTL and needs refresh.
	ErrCacheStale = errors.New("cache is stale")
)

// AKSMachineClienter provides operations for AKS machines.
type AKSMachineClienter interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, machineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
}

type opts struct {
	ttl             time.Duration
	pollInterval    time.Duration
	refreshInterval time.Duration
	disabled        bool
}

func defaultOpts() opts {
	return opts{
		ttl:             30 * time.Second,
		pollInterval:    5 * time.Second,
		refreshInterval: 5 * time.Minute,
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

// WithRefreshInterval sets the interval for the background worker to refresh the cache.
func WithRefreshInterval(d time.Duration) Option {
	return func(o opts) opts { o.refreshInterval = d; return o }
}

// WithCacheDisabled disables the cache entirely. All Get/List calls will return ErrCacheStale,
// forcing callers to fall through to direct API calls. No background goroutine is spawned.
func WithCacheDisabled() Option {
	return func(o opts) opts { o.disabled = true; return o }
}

// MachineCache caches AKS machine resources with TTL-based expiration and automatic background refresh.
type MachineCache struct {
	machines             sync.Map
	lastUpdatedUnixNanos atomic.Int64
	client               AKSMachineClienter

	clusterResourceGroup string
	clusterName          string
	aksMachinesPoolName  string

	updateRequests chan struct{}
	workerCtx      context.Context
	wg             sync.WaitGroup

	options opts
}

// NewMachineCache creates a new cache instance with a background worker for automatic refresh.
// By default, the cache will automatically refresh its contents in the background at the interval specified by the refreshInterval option.
// Updates to the cache are also triggered when a stale cache is accessed.
func NewMachineCache(ctx context.Context, client AKSMachineClienter, clusterResourceGroup, clusterName, aksMachinesPoolName string, opts ...Option) *MachineCache {
	cache := &MachineCache{
		client:               client,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
		updateRequests:       make(chan struct{}, 1),
		workerCtx:            ctx,
		options:              defaultOpts(),
	}

	for _, opt := range opts {
		cache.options = opt(cache.options)
	}

	if !cache.options.disabled {
		cache.wg.Add(1)
		go cache.run()
	}

	return cache
}

// Add stores a machine in the cache. This is useful for adding machines that were fetched directly
// from the API, bypassing the cache.
func (c *MachineCache) Add(machine *armcontainerservice.Machine) {
	if c.options.disabled {
		return
	}
	if machine != nil && machine.Name != nil {
		c.machines.Store(*machine.Name, machine)
	}
}

// Get retrieves a machine from the cache by name if the cache is fresh.
// Returns nil if the machine is not found in the cache.
// Returns ErrCacheStale if the cache is stale.
func (c *MachineCache) Get(machineName string) (*armcontainerservice.Machine, error) {
	if c.options.disabled || !c.isFresh() {
		c.requestUpdate()
		return nil, fmt.Errorf("%w for machine %q", ErrCacheStale, machineName)
	}

	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, nil
	}

	return value.(*armcontainerservice.Machine), nil
}

// List returns all cached machines, blocking until the cache is fresh.
func (c *MachineCache) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	if c.options.disabled || !c.isFresh() {
		c.requestUpdate()
		return nil, fmt.Errorf("%w while listing machines", ErrCacheStale)
	}

	machines := make([]*armcontainerservice.Machine, 0)
	c.machines.Range(func(key, value any) bool {
		machines = append(machines, value.(*armcontainerservice.Machine))
		return true
	})

	return machines, nil
}

// Invalidate removes a specific machine from the cache by name.
func (c *MachineCache) Invalidate(machineName string) {
	if c.options.disabled {
		return
	}
	c.machines.Delete(machineName)
}

// PollUntilDone polls for AKS machine provisioning completion using the cache.
// This polls indefinitely until the machine reaches a terminal state (Succeeded, Failed, or Deleting) or the context is canceled.
// If at any point the machine is not found, PollUntilDone will return an error.
func (c *MachineCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
	log.FromContext(ctx).V(2).Info("starting cache poller for AKS machine", "aksMachineName", name)

	// First check if the machine exists before starting to poll
	if err := c.checkMachineExists(ctx, name); err != nil {
		return nil, err
	}

	ticker := time.NewTicker(c.options.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while polling for AKS machine %q: %w", name, ctx.Err())

		case <-ticker.C:
			provisioningErr, pollerErr, done := c.pollOnce(ctx, name)
			if done {
				return provisioningErr, pollerErr
			}
		}
	}
}

func (c *MachineCache) checkMachineExists(ctx context.Context, name string) error {
	aksMachine, err := c.Get(name)
	if err == nil && aksMachine != nil {
		return nil
	}

	if err == nil || errors.Is(err, ErrCacheStale) {
		resp, apiErr := c.client.Get(ctx, c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, name, nil)
		if apiErr != nil {
			if machine.IsAKSMachineOrMachinesPoolNotFound(apiErr) {
				return fmt.Errorf("AKS machine %q does not exist", name)
			}
			return fmt.Errorf("failed to check if AKS machine %q exists: %w", name, apiErr)
		}

		machine := lo.ToPtr(resp.Machine)
		c.Add(machine)
		return nil
	}

	return err
}

func (c *MachineCache) pollOnce(ctx context.Context, aksMachineName string) (*armcontainerservice.ErrorDetail, error, bool) {
	aksMachine, err := c.Get(aksMachineName)
	if err != nil {
		if errors.Is(err, ErrCacheStale) {
			log.FromContext(ctx).V(1).Info("Cache poller: cache stale for AKS machine during poll", "aksMachineName", aksMachineName)
		} else {
			log.FromContext(ctx).Error(err, "Unexpected error while polling for AKS machine from cache", "aksMachineName", aksMachineName)
			return nil, err, true
		}
	}

	if aksMachine == nil {
		log.FromContext(ctx).V(1).Info("Cache poller: cache miss for AKS machine during poll", "aksMachineName", aksMachineName)
		return nil, nil, false
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

func (c *MachineCache) run() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.options.refreshInterval)
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

// update refreshes the machine cache by fetching the latest list of AKS machines from the Azure API.
// This should NOT be called directly; it is intended to be used by the background worker.
func (c *MachineCache) update(ctx context.Context) error {
	if c.isFresh() {
		return nil
	}
	nowTime := time.Now()
	defer func() {
		log.FromContext(ctx).Info("finished update for machine cache", "aksMachinesPoolName", c.aksMachinesPoolName, "duration", time.Since(nowTime).Seconds())
	}()

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

		log.FromContext(ctx).Info("fetched AKS machines page", "count", len(page.Value))
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
	return time.Since(lastUpdated) < c.options.ttl
}

func (c *MachineCache) requestUpdate() {
	select {
	case c.updateRequests <- struct{}{}:
	default:
	}
}

func isValid(ctx context.Context, properties *armcontainerservice.MachineProperties, machineName string) bool {
	if properties == nil || properties.Tags == nil {
		log.FromContext(ctx).Info("skipping AKS machine with nil properties or tags", "aksMachineName", machineName)
		return false
	}
	if _, hasTags := properties.Tags[launchtemplate.NodePoolTagKey]; !hasTags {
		log.FromContext(ctx).Info("skipping AKS machine without Karpenter nodepool tag", "aksMachineName", machineName)
		return false
	}

	return true
}
