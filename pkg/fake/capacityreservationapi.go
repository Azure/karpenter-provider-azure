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

package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
)

// CapacityReservationGroupsAPI is a fake for azapi.CapacityReservationGroupsAPI.
// By default it returns a zonal Targeted group in eastus with zones 1 and 2.
type CapacityReservationGroupsAPI struct {
	mu      sync.RWMutex
	GetFunc func(
		ctx context.Context,
		resourceGroupName string,
		capacityReservationGroupName string,
		options *armcompute.CapacityReservationGroupsClientGetOptions,
	) (armcompute.CapacityReservationGroupsClientGetResponse, error)
}

var _ azapi.CapacityReservationGroupsAPI = &CapacityReservationGroupsAPI{}

func (c *CapacityReservationGroupsAPI) Get(
	ctx context.Context,
	resourceGroupName string,
	capacityReservationGroupName string,
	options *armcompute.CapacityReservationGroupsClientGetOptions,
) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
	c.mu.RLock()
	getFunc := c.GetFunc
	c.mu.RUnlock()
	if getFunc != nil {
		return getFunc(ctx, resourceGroupName, capacityReservationGroupName, options)
	}
	return armcompute.CapacityReservationGroupsClientGetResponse{
		CapacityReservationGroup: armcompute.CapacityReservationGroup{
			ID: lo.ToPtr(fmt.Sprintf(
				"/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/capacityReservationGroups/%s",
				resourceGroupName, capacityReservationGroupName)),
			Name:     lo.ToPtr(capacityReservationGroupName),
			Location: lo.ToPtr("eastus"),
			Zones:    []*string{lo.ToPtr("1"), lo.ToPtr("2")},
			// Azure omits reservationType for Targeted groups, so the fake does too.
			Properties: &armcompute.CapacityReservationGroupProperties{},
		},
	}, nil
}

func (c *CapacityReservationGroupsAPI) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.GetFunc = nil
}

// CapacityReservationsAPI is a fake for azapi.CapacityReservationsAPI.
// By default it returns a single zone-1 Standard_D2s_v3 reservation.
type CapacityReservationsAPI struct {
	mu       sync.RWMutex
	ListFunc func(
		resourceGroupName string,
		capacityReservationGroupName string,
	) ([]*armcompute.CapacityReservation, error)
}

var _ azapi.CapacityReservationsAPI = &CapacityReservationsAPI{}

func (c *CapacityReservationsAPI) NewListByCapacityReservationGroupPager(
	resourceGroupName string,
	capacityReservationGroupName string,
	_ *armcompute.CapacityReservationsClientListByCapacityReservationGroupOptions,
) *runtime.Pager[armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse] {
	return runtime.NewPager(runtime.PagingHandler[armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse]{
		More: func(_ armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse) bool {
			return false
		},
		Fetcher: func(_ context.Context, _ *armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse) (armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse, error) {
			c.mu.RLock()
			listFunc := c.ListFunc
			c.mu.RUnlock()

			var reservations []*armcompute.CapacityReservation
			var err error
			if listFunc != nil {
				reservations, err = listFunc(resourceGroupName, capacityReservationGroupName)
				if err != nil {
					return armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse{}, err
				}
			} else {
				reservations = []*armcompute.CapacityReservation{
					NewCapacityReservation(resourceGroupName, capacityReservationGroupName, "reservation-1", "Standard_D2s_v3", 1, "1"),
				}
			}
			return armcompute.CapacityReservationsClientListByCapacityReservationGroupResponse{
				CapacityReservationListResult: armcompute.CapacityReservationListResult{Value: reservations},
			}, nil
		},
	})
}

func (c *CapacityReservationsAPI) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ListFunc = nil
}

// NewCapacityReservation builds a member reservation for tests. Passing no zones
// produces a regional reservation.
func NewCapacityReservation(resourceGroupName, groupName, name, vmSize string, capacity int64, zones ...string) *armcompute.CapacityReservation {
	return &armcompute.CapacityReservation{
		ID: lo.ToPtr(fmt.Sprintf(
			"/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/capacityReservationGroups/%s/capacityReservations/%s",
			resourceGroupName, groupName, name)),
		Name:     lo.ToPtr(name),
		Location: lo.ToPtr("eastus"),
		SKU: &armcompute.SKU{
			Name:     lo.ToPtr(vmSize),
			Capacity: lo.ToPtr(capacity),
		},
		Zones: lo.Map(zones, func(z string, _ int) *string { return lo.ToPtr(z) }),
		Properties: &armcompute.CapacityReservationProperties{
			ProvisioningState: lo.ToPtr("Succeeded"),
		},
	}
}
