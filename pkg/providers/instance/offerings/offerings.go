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

	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// Suggestion: consider merging this package with instancetype package, as both of their responsibilities deal with instance types management

// SkuSizePriorityZone represents a single candidate offering: a specific instance type, capacity type (priority), and zone.
// Used to provide a ranked list of alternatives for SKU substitution when the first choice has no capacity.
type SkuSizePriorityZone struct {
	InstanceType *corecloudprovider.InstanceType
	Priority     string // karpv1.CapacityTypeSpot or karpv1.CapacityTypeOnDemand
	Zone         string
}

// PickOrderedSkuSizePriorityAndZone returns a ranked list of (instanceType, priority, zone) candidates
// from the given instance types and their offerings, ordered by price (cheapest first).
// The instance types are expected to be pre-sorted by cheapest offering price.
// For each instance type, all compatible available offerings matching the nodeClaim requirements
// are included, producing one entry per unique (instanceType, priority, zone) combination.
// Returns an empty slice if no compatible offerings are found.
func PickOrderedSkuSizePriorityAndZone(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) []SkuSizePriorityZone {
	if len(instanceTypes) == 0 {
		return nil
	}

	requestedZones := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...).Get(v1.LabelTopologyZone)

	var result []SkuSizePriorityZone
	for _, instanceType := range instanceTypes {
		priority := getPriorityForInstanceType(nodeClaim, instanceType)
		priorityOfferings := lo.Filter(instanceType.Offerings.Available(), func(o *corecloudprovider.Offering, _ int) bool {
			return getOfferingCapacityType(o) == priority && requestedZones.Has(getOfferingZone(o))
		})
		// Deduplicate zones for this instance type + priority (a zone may appear in multiple offerings)
		seenZones := sets.New[string]()
		for _, offering := range priorityOfferings {
			zone := getOfferingZone(offering)
			if seenZones.Has(zone) {
				continue
			}
			seenZones.Insert(zone)
			result = append(result, SkuSizePriorityZone{
				InstanceType: instanceType,
				Priority:     priority,
				Zone:         zone,
			})
		}
	}

	if len(result) > 0 {
		log.FromContext(ctx).Info("selected instance type candidates",
			"count", len(result),
			logging.InstanceType, result[0].InstanceType.Name)
	}

	return result
}

// PickSkuSizePriorityAndZone picks the single "best" SKU, priority and zone from InstanceType options.
// This is a convenience wrapper around PickOrderedSkuSizePriorityAndZone that returns the first (best) candidate.
func PickSkuSizePriorityAndZone(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	instanceTypes []*corecloudprovider.InstanceType,
) (*corecloudprovider.InstanceType, string, string) {
	candidates := PickOrderedSkuSizePriorityAndZone(ctx, nodeClaim, instanceTypes)
	if len(candidates) == 0 {
		return nil, "", ""
	}
	return candidates[0].InstanceType, candidates[0].Priority, candidates[0].Zone
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
