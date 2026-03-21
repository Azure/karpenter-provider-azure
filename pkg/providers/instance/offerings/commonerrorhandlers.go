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

package offerings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/skewer"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

var (
	SubscriptionQuotaReachedReason              = "SubscriptionQuotaReached"
	AllocationFailureReason                     = "AllocationFailure"
	ZonalAllocationFailureReason                = "ZonalAllocationFailure"
	OverconstrainedZonalAllocationFailureReason = "OverconstrainedZonalAllocationFailure"
	OverconstrainedAllocationFailureReason      = "OverconstrainedAllocationFailure"
	SKUNotAvailableReason                       = "SKUNotAvailable"

	// LowQuotaTTL is the TTL for offerings that return a quota error but the quota limit is not 0.
	// This means there is still some quota available, just not enough to fulfill the current request.
	// We set this to some value "reasonably lower" than SubscriptionQuotaReachedTTL.
	// It needs to be long enough that we can attempt multiple sizes in the same family in descending order and still have the entry
	// in the cache when we get to the smaller sizes, to allow failover to another family.
	// For example, if we're trying to fit 64 cores worth of pods and we only have quota for 10 cores of Dv2:
	// D64_v2  -> D32_v2 -> D16_v2 all fail before one D8_v2 finally succeeds, but we need more cores than that so we proceed on to D4_v2 and D2_v2 (one of which succeeds).
	// This effectively utilizes all the quota, but requires that we don't have the D64 cache entry expire before we've tried all the sizes in the family down to the one that works,
	// otherwise we might allocate a D8_v2 and then go back and try D64_v2 again - in the worst case we can get caught in a "loop" doing this and never actually proceed to a size
	// that works.
	// TODO: If/when we factor in actual quota API usage we may be able to reduce this TTL and use the quota API data to inform which sizes we try first.
	LowQuotaTTL = 10 * time.Minute
	// SubscriptionQuotaReachedTTL is the TTL for offerings that return a quota error with a limit of 0, meaning there is no quota available for that SKU family at all in the subscription.
	// This is often the case if the user doesn't have any quota for that offering at all, hence the longer TTL.
	// TODO: If/when we factor in actual quota API usage in the future we may be able to get rid of this longer TTL and just rely on LowQuotaTTL.
	SubscriptionQuotaReachedTTL = 1 * time.Hour
	// AllocationFailureTTL is the TTL for offerings that returned an allocation failure from Azure. AllocationFailure usually means that there is a capacity
	// crunch of some kind (for that zone, for that offering, etc). It's unlikely that the capacity crunch resolves itself in a very short amount of time, hence the longer TTL.
	AllocationFailureTTL = 1 * time.Hour
	// SKUNotAvailableSpotTTL can happen if the SKU isn't available at all, OR if it's out of capacity for Spot.
	// In the first case we'd ideally set a longer TTL like we do for on-demand below, but in the other case it may resolve more quickly,
	// so we set a 1h TTL.
	SKUNotAvailableSpotTTL = 1 * time.Hour
	// SKUNotAvailableOnDemandTTL is the TTL for SKU not available errors for on-demand capacity.
	// This generally indicates that the SKU is not available in the region. It is unlikely to resolve quickly so we set a very long TTL.
	SKUNotAvailableOnDemandTTL = 23 * time.Hour
)

type errorHandle func(ctx context.Context, unavailableOfferings *cache.UnavailableOfferings, sku *skewer.SKU, instanceType *corecloudprovider.InstanceType, zone, capacityType, errorCode, errorMessage string) error

// markOfferingsUnavailableForCapacityType marks all offerings of the specified capacity type as unavailable
func markOfferingsUnavailableForCapacityType(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	reason string,
	ttl time.Duration,
) {
	for _, offering := range instanceType.Offerings {
		if getOfferingCapacityType(offering) != capacityType {
			continue
		}
		unavailableOfferings.MarkUnavailableWithTTL(ctx, reason, sku, getOfferingZone(offering), capacityType, ttl)
	}
}

// markAllZonesUnavailableForBothCapacityTypes marks all unique zones as unavailable for both on-demand and spot
func markAllZonesUnavailableForBothCapacityTypes(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	reason string,
	ttl time.Duration,
) {
	// instanceType.Offerings contains multiple entries for one zone, but we only care that zone appears at least once
	zonesToBlock := make(map[string]struct{})
	for _, offering := range instanceType.Offerings {
		offeringZone := getOfferingZone(offering)
		zonesToBlock[offeringZone] = struct{}{}
	}
	for zone := range zonesToBlock {
		unavailableOfferings.MarkUnavailableWithTTL(ctx, reason, sku, zone, karpv1.CapacityTypeOnDemand, ttl)
		unavailableOfferings.MarkUnavailableWithTTL(ctx, reason, sku, zone, karpv1.CapacityTypeSpot, ttl)
	}
}

func handleLowPriorityQuotaError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	// Mark in cache that spot quota has been reached for this subscription
	unavailableOfferings.MarkSpotUnavailableWithTTL(ctx, SubscriptionQuotaReachedTTL)
	return fmt.Errorf("this subscription has reached the regional vCPU quota for spot (LowPriorityQuota). To scale beyond this limit, please review the quota increase process here: https://docs.microsoft.com/en-us/azure/azure-portal/supportability/low-priority-quota")
}

func handleSKUFamilyQuotaError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	// Subscription quota has been reached for this VM SKU, mark the instance type as unavailable in all zones available to the offering
	// This will also update the TTL for an existing offering in the cache that is already unavailable

	for _, offering := range instanceType.Offerings {
		if getOfferingCapacityType(offering) != capacityType {
			continue
		}
		// If we have a quota limit of 0 vcpus, we mark the offerings unavailable for an hour.
		// CPU limits of 0 are usually due to a subscription having no allocated quota for that instance type at all on the subscription.
		if cpuLimitIsZero(errorMessage) {
			unavailableOfferings.MarkUnavailableWithTTL(ctx, SubscriptionQuotaReachedReason, sku, getOfferingZone(offering), capacityType, SubscriptionQuotaReachedTTL)
		} else {
			unavailableOfferings.MarkUnavailableWithTTL(ctx, SubscriptionQuotaReachedReason, sku, getOfferingZone(offering), capacityType, LowQuotaTTL)
		}
	}
	return fmt.Errorf("subscription level %s vCPU quota for %s has been reached (may try provision an alternative instance type)", capacityType, instanceType.Name)
}

func handleSKUNotAvailableError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	// https://aka.ms/azureskunotavailable: either not available for a location or zone, or out of capacity for Spot.
	// We only expect to observe the Spot case, not location or zone restrictions, because:
	// - SKUs with location restriction are already filtered out via sku.HasLocationRestriction
	// - zonal restrictions are filtered out internally by sku.AvailabilityZones, and don't get offerings
	skuNotAvailableTTL := SKUNotAvailableSpotTTL
	if capacityType == karpv1.CapacityTypeOnDemand { // should not happen, defensive check
		skuNotAvailableTTL = SKUNotAvailableOnDemandTTL // still mark all offerings as unavailable, but with a longer TTL
	}
	// mark the instance type as unavailable for all offerings/zones for the capacity type
	markOfferingsUnavailableForCapacityType(ctx, unavailableOfferings, sku, instanceType, capacityType, SKUNotAvailableReason, skuNotAvailableTTL)

	return fmt.Errorf(
		"the requested SKU is unavailable for instance type %s in zone %s with capacity type %s, for more details please visit: https://aka.ms/azureskunotavailable",
		instanceType.Name,
		zone,
		capacityType)
}

// For zonal allocation failure, we will mark all instance types from this SKU family that have >= CPU count as the one that hit the error in this zone
func handleZonalAllocationFailureError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	_,
	errorCode,
	errorMessage string,
) error {
	unavailableOfferings.MarkUnavailableWithTTL(ctx, ZonalAllocationFailureReason, sku, zone, karpv1.CapacityTypeOnDemand, AllocationFailureTTL)
	unavailableOfferings.MarkUnavailableWithTTL(ctx, ZonalAllocationFailureReason, sku, zone, karpv1.CapacityTypeSpot, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources in the selected zone (%s). (will try a different zone to fulfill your request)", zone)
}

// AllocationFailure means that VM allocation to the dedicated host has failed. But it can also mean "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."
func handleAllocationFailureError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	markAllZonesUnavailableForBothCapacityTypes(ctx, unavailableOfferings, sku, instanceType, AllocationFailureReason, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources with selected VM size (%s). (will try a different VM size to fulfill your request)", instanceType.Name)
}

// OverconstrainedZonalAllocationFailure means that specific zone cannot accommodate the selected size and capacity combination.
func handleOverconstrainedZonalAllocationFailureError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	// OverconstrainedZonalAllocationFailure means that specific zone cannot accommodate the selected size and capacity combination.
	unavailableOfferings.MarkUnavailableWithTTL(ctx, OverconstrainedZonalAllocationFailureReason, sku, zone, capacityType, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources in the selected zone (%s) with %s capacity type and %s VM size. (will try a different zone, capacity type or VM size to fulfill your request)", zone, capacityType, instanceType.Name)
}

// OverconstrainedAllocationFailure means that all zones cannot accommodate the selected size and capacity combination.
func handleOverconstrainedAllocationFailureError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	markOfferingsUnavailableForCapacityType(ctx, unavailableOfferings, sku, instanceType, capacityType, OverconstrainedAllocationFailureReason, AllocationFailureTTL)

	return fmt.Errorf("unable to allocate resources in all zones with %s capacity type and %s VM size. (will try a different capacity type or VM size to fulfill your request)", capacityType, instanceType.Name)
}

func handleRegionalQuotaError(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	sku *skewer.SKU,
	instanceType *corecloudprovider.InstanceType,
	zone,
	capacityType,
	errorCode,
	errorMessage string,
) error {
	// InsufficientCapacityError is appropriate here because trying any other instance type will not help
	return corecloudprovider.NewInsufficientCapacityError(
		fmt.Errorf(
			"regional %s vCPU quota limit for subscription has been reached. To scale beyond this limit, please review the quota increase process here: https://learn.microsoft.com/en-us/azure/quotas/regional-quota-requests",
			capacityType))
}

func cpuLimitIsZero(errorMessage string) bool {
	return strings.Contains(errorMessage, "Current Limit: 0")
}
