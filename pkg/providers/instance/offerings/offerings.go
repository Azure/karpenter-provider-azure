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
	"math"
	"sort"

	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	"github.com/Azure/skewer"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// Suggestion: consider merging this package with instancetype package, as both of their responsibilities deal with instance types management

// Pick the "best" SKU, priority and zone, from InstanceType options (and their offerings) in the request
func PickSkuSizePriorityAndZone(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*corecloudprovider.InstanceType, string, string) {
	if len(instanceTypes) == 0 {
		return nil, "", ""
	}
	// InstanceType/VM SKU - just pick the first one for now. They are presorted by cheapest offering price (taking node requirements into account)
	instanceType := instanceTypes[0]
	log.FromContext(ctx).Info("selected instance type", logging.InstanceType, instanceType.Name)
	// Priority - Nodepool defaults to Regular, so pick Spot if it is explicitly included in requirements (and is offered in at least one zone)
	priority := getPriorityForInstanceType(nodeClaim, instanceType)
	// Zone - ideally random/spread from requested zones that support given Priority
	requestedZones := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...).Get(v1.LabelTopologyZone)
	priorityOfferings := lo.Filter(instanceType.Offerings.Available(), func(o *corecloudprovider.Offering, _ int) bool {
		return getOfferingCapacityType(o) == priority && requestedZones.Has(getOfferingZone(o))
	})
	zonesWithPriority := lo.Map(priorityOfferings, func(o *corecloudprovider.Offering, _ int) string { return getOfferingZone(o) })
	if zone, ok := sets.New(zonesWithPriority...).PopAny(); ok {
		return instanceType, priority, zone
	}
	return nil, "", ""
}

// PreLaunchFilter re-checks instance types against the live unavailable offerings cache at launch time.
// This is a "circuit breaker" that prevents wasted Azure API calls during large-scale scheduling:
//
// During a prompt scale (e.g., 5000 cores), the scheduler creates many NodeClaims simultaneously.
// The offerings Available flags were set at scheduling time (in createOfferings), but by launch time,
// some SKUs may have been marked unavailable by parallel failures (e.g., AllocationFailed, quota exhausted).
// Without this check, all NodeClaims would attempt VM creation and fail, wasting API calls.
//
// With this check, once the first failure updates the ICE cache, subsequent NodeClaims skip the
// failed SKU immediately, reducing wasted calls from ~78 to ~5-10 (depending on race timing).
//
// The isAvailable function checks if a given instance type + zone + capacity type is still available
// in the live ICE cache. If isAvailable is nil, this function returns the input unfiltered (fail-open).
func PreLaunchFilter(
	ctx context.Context,
	instanceTypes []*corecloudprovider.InstanceType,
	isAvailable func(instanceTypeName, zone, capacityType string) bool,
) []*corecloudprovider.InstanceType {
	if isAvailable == nil {
		return instanceTypes
	}

	var filtered []*corecloudprovider.InstanceType
	for _, it := range instanceTypes {
		// Check if ANY offering for this instance type is still available in the live cache
		hasAvailable := false
		for _, offering := range it.Offerings.Available() {
			zone := getOfferingZone(offering)
			capacityType := getOfferingCapacityType(offering)
			if isAvailable(it.Name, zone, capacityType) {
				hasAvailable = true
				break
			}
		}

		if hasAvailable {
			filtered = append(filtered, it)
		} else {
			log.FromContext(ctx).V(1).Info("pre-launch filter: skipping instance type, all offerings now unavailable in live cache",
				"instanceType", it.Name)
		}
	}

	return filtered
}

// NewLiveCacheAvailabilityCheck creates an isAvailable callback for PreLaunchFilter
// that checks the live unavailable offerings cache. This avoids duplicating the
// cache-lookup logic in every provider that calls PreLaunchFilter.
func NewLiveCacheAvailabilityCheck(
	ctx context.Context,
	unavailableOfferings *cache.UnavailableOfferings,
	getSKU func(ctx context.Context, instanceTypeName string) (*skewer.SKU, error),
) func(instanceTypeName, zone, capacityType string) bool {
	if unavailableOfferings == nil || getSKU == nil {
		return nil // PreLaunchFilter treats nil as fail-open
	}
	return func(instanceTypeName, zone, capacityType string) bool {
		sku, err := getSKU(ctx, instanceTypeName)
		if err != nil {
			return true // fail open
		}
		return !unavailableOfferings.IsUnavailable(sku, zone, capacityType)
	}
}

// getPriorityForInstanceType selects spot if both constraints are flexible and there is an available offering.
// The Azure Cloud Provider defaults to Regular, so spot must be explicitly included in capacity type requirements.
//
// This returns from a single pre-selected InstanceType, rather than all InstanceType options in nodeRequest,
// because Azure Cloud Provider does client-side selection of particular InstanceType from options
func getPriorityForInstanceType(nodeClaim *karpv1.NodeClaim, instanceType *corecloudprovider.InstanceType) string {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)

	if requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
		for _, offering := range instanceType.Offerings.Available() {
			if requirements.Get(v1.LabelTopologyZone).Has(getOfferingZone(offering)) && getOfferingCapacityType(offering) == karpv1.CapacityTypeSpot {
				return karpv1.CapacityTypeSpot
			}
		}
	}
	return karpv1.CapacityTypeOnDemand
}

func OrderInstanceTypesByPrice(instanceTypes []*corecloudprovider.InstanceType, requirements scheduling.Requirements) []*corecloudprovider.InstanceType {
	// Order instance types so that we get the cheapest instance types of the available offerings
	sort.Slice(instanceTypes, func(i, j int) bool {
		iPrice := math.MaxFloat64
		jPrice := math.MaxFloat64
		if len(instanceTypes[i].Offerings.Available().Compatible(requirements)) > 0 {
			iPrice = instanceTypes[i].Offerings.Available().Compatible(requirements).Cheapest().Price
		}
		if len(instanceTypes[j].Offerings.Available().Compatible(requirements)) > 0 {
			jPrice = instanceTypes[j].Offerings.Available().Compatible(requirements).Cheapest().Price
		}
		if iPrice == jPrice {
			return instanceTypes[i].Name < instanceTypes[j].Name
		}
		return iPrice < jPrice
	})
	return instanceTypes
}

func getOfferingCapacityType(offering *corecloudprovider.Offering) string {
	return offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
}

func getOfferingZone(offering *corecloudprovider.Offering) string {
	return offering.Requirements.Get(v1.LabelTopologyZone).Any()
}

// May return nil if there is no match
func GetInstanceTypeFromVMSize(vmSize string, possibleInstanceTypes []*corecloudprovider.InstanceType) *corecloudprovider.InstanceType {
	instanceType, _ := lo.Find(possibleInstanceTypes, func(i *corecloudprovider.InstanceType) bool {
		return i.Name == vmSize
	})

	return instanceType
}
