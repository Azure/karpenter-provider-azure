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
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	// DefaultUpdateInterval is how often the background worker updates the cache
	DefaultUpdateInterval = 1 * time.Minute

	// DefaultCacheExpiration is how long we consider the cache valid before returning errors
	DefaultCacheExpiration = 10 * time.Minute

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

type AKSMachiner interface {
	NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse]
	Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error)
}

// MachineListCache is a simple background-refreshed cache of AKS machines.
// A background worker updates the cache every minute using the select client (minimal fields).
// When machines have failed provisioning state, it fetches full details using the full client.
// Get/List operations simply read from the cache - no on-demand updates.
// The cache is considered expired if the last update was over 10 minutes ago.
type MachineListCache struct {
	machines             sync.Map     // keyed by machine name, value is *armcontainerservice.Machine
	lastUpdatedUnixNanos atomic.Int64 // nanoseconds since epoch; 0 means never updated

	lightClient AKSMachiner
	fullClient  AKSMachiner

	clusterResourceGroup string
	clusterName          string
	aksMachinesPoolName  string

	// Background worker
	workerCtx    context.Context
	workerCancel context.CancelFunc
	wg           sync.WaitGroup
}

func NewMachineListCache(ctx context.Context, lightClient AKSMachiner, fullClient AKSMachiner, clusterResourceGroup, clusterName, aksMachinesPoolName string) *MachineListCache {
	workerCtx, workerCancel := context.WithCancel(ctx)

	cache := &MachineListCache{
		lightClient:          lightClient,
		fullClient:           fullClient,
		clusterResourceGroup: clusterResourceGroup,
		clusterName:          clusterName,
		aksMachinesPoolName:  aksMachinesPoolName,
		workerCtx:            workerCtx,
		workerCancel:         workerCancel,
	}

	// Start background worker
	cache.wg.Add(1)
	go cache.updateWorker()

	return cache
}

// isExpired returns true if the cache hasn't been updated in over 10 minutes
func (c *MachineListCache) isExpired() bool {
	lastUpdatedNanos := c.lastUpdatedUnixNanos.Load()
	if lastUpdatedNanos == 0 {
		return true // never updated
	}
	lastUpdated := time.Unix(0, lastUpdatedNanos)
	return time.Since(lastUpdated) > DefaultCacheExpiration
}

// Get retrieves a machine from the cache by name.
// Returns an error if the cache is expired or the machine is not found.
func (c *MachineListCache) Get(machineName string) (*armcontainerservice.Machine, error) {
	if c.isExpired() {
		return nil, fmt.Errorf("cache is expired for machine %q", machineName)
	}

	value, ok := c.machines.Load(machineName)
	if !ok {
		return nil, fmt.Errorf("machine %q not found in cache", machineName)
	}

	return value.(*armcontainerservice.Machine), nil
}

// List returns all machines in the cache.
// Returns an error if the cache is expired.
func (c *MachineListCache) List(ctx context.Context) ([]*armcontainerservice.Machine, error) {
	if c.isExpired() {
		return nil, fmt.Errorf("cache is expired")
	}

	machines := make([]*armcontainerservice.Machine, 0)
	c.machines.Range(func(key, value any) bool {
		machines = append(machines, value.(*armcontainerservice.Machine))
		return true
	})

	return machines, nil
}

// Shutdown stops the background update worker and waits for it to finish.
func (c *MachineListCache) Shutdown() {
	c.workerCancel()
	c.wg.Wait()
}

// updateWorker runs in a background goroutine and updates the cache every minute.
func (c *MachineListCache) updateWorker() {
	defer c.wg.Done()

	ticker := time.NewTicker(DefaultUpdateInterval)
	defer ticker.Stop()

	// Do an immediate update on startup
	if err := c.update(c.workerCtx); err != nil {
		log.FromContext(c.workerCtx).Error(err, "initial cache update failed")
	}

	for {
		select {
		case <-c.workerCtx.Done():
			return

		case <-ticker.C:
			if err := c.update(c.workerCtx); err != nil {
				log.FromContext(c.workerCtx).Error(err, "periodic cache update failed")
			}
		}
	}
}

func (c *MachineListCache) update(ctx context.Context) error {
	log.FromContext(ctx).Info("updating machine list cache", "aksMachinesPoolName", c.aksMachinesPoolName)

	now := time.Now()
	defer func() {
		log.FromContext(ctx).Info("finished updating machine list cache",
			"duration", time.Since(now).String(),
			"aksMachinesPoolName", c.aksMachinesPoolName,
		)
	}()

	// Use select client for LIST (minimal fields)
	pager := c.lightClient.NewListPager(c.clusterResourceGroup, c.clusterName, c.aksMachinesPoolName, nil)
	if pager == nil {
		return fmt.Errorf("failed to list AKS machines: created pager is nil")
	}

	fetchedMachineNames := make(map[string]struct{})
	totalMachinesStored := 0

	for pager.More() {
		now := time.Now()
		page, err := pager.NextPage(ctx)
		log.FromContext(ctx).V(1).Info("fetched page of AKS machines",
			"duration", time.Since(now).Seconds(),
			"aksMachinesPoolName", c.aksMachinesPoolName,
			"pageSize", len(page.Value))
		if err != nil {
			if isAKSMachineOrMachinesPoolNotFound(err) {
				log.FromContext(ctx).V(1).Info("AKS machines pool not found, treating as no machines")
				break
			}
			return fmt.Errorf("failed to list AKS machines: %w", err)
		}

		for _, aksMachine := range page.Value {
			// Filter to only include machines created by Karpenter
			if aksMachine.Properties != nil && aksMachine.Properties.Tags != nil {
				if _, hasKarpenterTag := aksMachine.Properties.Tags[nodePoolTagKey]; hasKarpenterTag {
					if aksMachine.Name != nil {
						c.machines.Store(*aksMachine.Name, aksMachine)
						fetchedMachineNames[*aksMachine.Name] = struct{}{}
						totalMachinesStored++
					}
				}
			}
		}
	}

	log.FromContext(ctx).Info("completed LIST of AKS machines",
		"totalMachines", totalMachinesStored,
		"aksMachinesPoolName", c.aksMachinesPoolName,
	)

	// Remove stale entries
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

func (c *MachineListCache) PollUntilDone(ctx context.Context, name string) (*armcontainerservice.ErrorDetail, error) {
	log.FromContext(ctx).Info("starting cache poller for AKS machine", "aksMachineName", name)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while polling for AKS machine %q", name)

		case <-ticker.C:
			// Check if cache is expired
			if c.isExpired() {
				log.FromContext(ctx).V(1).Info("cache expired during poll, waiting for update", "aksMachineName", name)
				continue
			}

			// Get machine from cache
			machine, ok := c.machines.Load(name)
			if !ok {
				log.FromContext(ctx).V(1).Info("machine not in cache, waiting", "aksMachineName", name)
				continue
			}

			aksMachine := machine.(*armcontainerservice.Machine)
			if aksMachine.Properties == nil || aksMachine.Properties.ProvisioningState == nil {
				log.FromContext(ctx).V(1).Info("nil provisioning state, waiting", "aksMachineName", name)
				continue
			}

			provisioningState := *aksMachine.Properties.ProvisioningState
			switch provisioningState {
			case consts.ProvisioningStateCreating, consts.ProvisioningStateUpdating:
				log.FromContext(ctx).V(2).Info("machine provisioning ongoing", "aksMachineName", name, "state", provisioningState)
				continue

			case consts.ProvisioningStateDeleting:
				return nil, fmt.Errorf("AKS machine %q sees canceled provisioning state %s", name, provisioningState)

			case consts.ProvisioningStateSucceeded:
				return nil, nil

			case consts.ProvisioningStateFailed:
				if aksMachine.Properties.Status != nil && aksMachine.Properties.Status.ProvisioningError != nil {
					return aksMachine.Properties.Status.ProvisioningError, nil
				}
				return nil, fmt.Errorf("AKS machine %q sees fatal provisioning state %s, but ProvisioningError is nil", name, provisioningState)

			default:
				log.FromContext(ctx).V(1).Info("unrecognized provisioning state, waiting", "aksMachineName", name, "state", provisioningState)
				continue
			}
		}
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
