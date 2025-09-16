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
	"fmt"
	"strings"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/skewer"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type responseErrorHandler struct {
	name                string
	matchError          func(error) bool
	handleResponseError func(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, responseError error) error
}

// markOfferingsUnavailableForCapacityType marks all offerings of the specified capacity type as unavailable
func markOfferingsUnavailableForCapacityType(ctx context.Context, provider *DefaultProvider, instanceType *corecloudprovider.InstanceType, capacityType string, reason string, ttl time.Duration) {
	for _, offering := range instanceType.Offerings {
		if getOfferingCapacityType(offering) != capacityType {
			continue
		}
		provider.unavailableOfferings.MarkUnavailableWithTTL(ctx, reason, instanceType.Name, getOfferingZone(offering), capacityType, ttl)
	}
}

// markAllZonesUnavailableForBothCapacityTypes marks all unique zones as unavailable for both on-demand and spot
func markAllZonesUnavailableForBothCapacityTypes(ctx context.Context, provider *DefaultProvider, instanceType *corecloudprovider.InstanceType, reason string, ttl time.Duration) {
	// instanceType.Offerings contains multiple entries for one zone, but we only care that zone appears at least once
	zonesToBlock := make(map[string]struct{})
	for _, offering := range instanceType.Offerings {
		offeringZone := getOfferingZone(offering)
		zonesToBlock[offeringZone] = struct{}{}
	}
	for zone := range zonesToBlock {
		provider.unavailableOfferings.MarkUnavailableWithTTL(ctx, reason, instanceType.Name, zone, karpv1.CapacityTypeOnDemand, ttl)
		provider.unavailableOfferings.MarkUnavailableWithTTL(ctx, reason, instanceType.Name, zone, karpv1.CapacityTypeSpot, ttl)
	}
}

func defaultResponseErrorHandlers() []responseErrorHandler {
	return []responseErrorHandler{
		{
			name:                "LowPriorityQuotaHasBeenReached",
			matchError:          sdkerrors.LowPriorityQuotaHasBeenReached,
			handleResponseError: handleLowPriorityQuotaError,
		},
		{
			name:                "SKUFamilyQuotaHasBeenReached",
			matchError:          sdkerrors.SKUFamilyQuotaHasBeenReached,
			handleResponseError: handleSKUFamilyQuotaError,
		},
		{
			name:                "IsSKUNotAvailable",
			matchError:          sdkerrors.IsSKUNotAvailable,
			handleResponseError: handleSKUNotAvailableError,
		},
		{
			name:                "ZonalAllocationFailureOccurred",
			matchError:          sdkerrors.ZonalAllocationFailureOccurred,
			handleResponseError: handleZonalAllocationFailureError,
		},
		{
			name:                "AllocationFailureOccurred",
			matchError:          sdkerrors.AllocationFailureOccurred,
			handleResponseError: handleAllocationFailureError,
		},
		{
			name:                "OverconstrainedZonalAllocationFailureOccurred",
			matchError:          sdkerrors.OverconstrainedZonalAllocationFailureOccurred,
			handleResponseError: handleOverconstrainedZonalAllocationFailureError,
		},
		{
			name:                "OverconstrainedAllocationFailureOccurred",
			matchError:          sdkerrors.OverconstrainedAllocationFailureOccurred,
			handleResponseError: handleOverconstrainedAllocationFailureError,
		},
		{
			name:                "RegionalQuotaHasBeenReached",
			matchError:          sdkerrors.RegionalQuotaHasBeenReached,
			handleResponseError: handleRegionalQuotaError,
		},
	}
}

func handleLowPriorityQuotaError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	// Mark in cache that spot quota has been reached for this subscription
	provider.unavailableOfferings.MarkSpotUnavailableWithTTL(ctx, SubscriptionQuotaReachedTTL)
	return fmt.Errorf("this subscription has reached the regional vCPU quota for spot (LowPriorityQuota). To scale beyond this limit, please review the quota increase process here: https://docs.microsoft.com/en-us/azure/azure-portal/supportability/low-priority-quota")
}

func handleSKUFamilyQuotaError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	// Subscription quota has been reached for this VM SKU, mark the instance type as unavailable in all zones available to the offering
	// This will also update the TTL for an existing offering in the cache that is already unavailable

	for _, offering := range instanceType.Offerings {
		if getOfferingCapacityType(offering) != capacityType {
			continue
		}
		// If we have a quota limit of 0 vcpus, we mark the offerings unavailable for an hour.
		// CPU limits of 0 are usually due to a subscription having no allocated quota for that instance type at all on the subscription.
		if cpuLimitIsZero(err) {
			provider.unavailableOfferings.MarkUnavailableWithTTL(ctx, SubscriptionQuotaReachedReason, instanceType.Name, getOfferingZone(offering), capacityType, SubscriptionQuotaReachedTTL)
		} else {
			provider.unavailableOfferings.MarkUnavailable(ctx, SubscriptionQuotaReachedReason, instanceType.Name, getOfferingZone(offering), capacityType)
		}
	}
	return fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", capacityType, instanceType.Name)
}

func handleSKUNotAvailableError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	// https://aka.ms/azureskunotavailable: either not available for a location or zone, or out of capacity for Spot.
	// We only expect to observe the Spot case, not location or zone restrictions, because:
	// - SKUs with location restriction are already filtered out via sku.HasLocationRestriction
	// - zonal restrictions are filtered out internally by sku.AvailabilityZones, and don't get offerings
	skuNotAvailableTTL := SKUNotAvailableSpotTTL
	if capacityType == karpv1.CapacityTypeOnDemand { // should not happen, defensive check
		skuNotAvailableTTL = SKUNotAvailableOnDemandTTL // still mark all offerings as unavailable, but with a longer TTL
	}
	// mark the instance type as unavailable for all offerings/zones for the capacity type
	markOfferingsUnavailableForCapacityType(ctx, provider, instanceType, capacityType, SKUNotAvailableReason, skuNotAvailableTTL)

	return fmt.Errorf(
		"the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable",
		instanceType.Name,
		zone,
		capacityType)
}

// For zonal allocation failure, we will mark all instance types from this SKU family that have >= CPU count as the one that hit the error in this zone
func handleZonalAllocationFailureError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	vCPU, vCPUErr := sku.VCPU() // versionedSKUFamily e.g. "N4" for "NV8as_v4"
	if vCPUErr != nil {
		// default to 0 if we can't determine VCPU count, this shouldn't happen as long as data in skewer.SKU is correct
		vCPU = 0
	}
	provider.unavailableOfferings.MarkFamilyUnavailableAtCPUCount(ctx, sku.GetFamilyName(), zone, karpv1.CapacityTypeOnDemand, vCPU, AllocationFailureTTL)
	provider.unavailableOfferings.MarkFamilyUnavailableAtCPUCount(ctx, sku.GetFamilyName(), zone, karpv1.CapacityTypeSpot, vCPU, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)", zone)
}

// AllocationFailure means that VM allocation to the dedicated host has failed. But it can also mean "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."
func handleAllocationFailureError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	markAllZonesUnavailableForBothCapacityTypes(ctx, provider, instanceType, AllocationFailureReason, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources with selected VM size (%s). (will try a different VM size to fulfill your request)", instanceType.Name)
}

// OverconstrainedZonalAllocationFailure means that specific zone cannot accommodate the selected size and capacity combination.
func handleOverconstrainedZonalAllocationFailureError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	// OverconstrainedZonalAllocationFailure means that specific zone cannot accommodate the selected size and capacity combination.
	provider.unavailableOfferings.MarkUnavailableWithTTL(ctx, OverconstrainedZonalAllocationFailureReason, instanceType.Name, zone, capacityType, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources in the selected zone (%s) with %s capacity type and %s VM size. (will try a different zone, capacity type or VM size to fulfill your request)", zone, capacityType, instanceType.Name)
}

// OverconstrainedAllocationFailure means that all zones cannot accommodate the selected size and capacity combination.
func handleOverconstrainedAllocationFailureError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	markOfferingsUnavailableForCapacityType(ctx, provider, instanceType, capacityType, OverconstrainedAllocationFailureReason, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources in all zones with %s capacity type and %s VM size. (will try a different capacity type or VM size to fulfill your request)", capacityType, instanceType.Name)
}

func handleRegionalQuotaError(ctx context.Context, provider *DefaultProvider, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType string, err error) error {
	// InsufficientCapacityError is appropriate here because trying any other instance type will not help
	return corecloudprovider.NewInsufficientCapacityError(
		fmt.Errorf(
			"regional %s vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests",
			capacityType))
}

func cpuLimitIsZero(err error) bool {
	return strings.Contains(err.Error(), "Current Limit: 0")
}
