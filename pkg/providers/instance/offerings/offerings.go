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

	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	allocationstrategy "github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// Suggestion: consider merging this package with instancetype package, as both of their responsibilities deal with instance types management

// Pick the "best" SKU, priority and zone, from pre-filtered and sorted InstanceOfferings.
// instanceOfferings should already be filtered by availability/compatibility and sorted by cheapest offering price.
func PickSkuSizePriorityAndZone(
	ctx context.Context,
	instanceOfferings []allocationstrategy.InstanceOffering,
) (*corecloudprovider.InstanceType, string, string) {
	if len(instanceOfferings) == 0 {
		return nil, "", ""
	}
	// InstanceType/VM SKU - pick the first one (cheapest after filtering/sorting)
	best := instanceOfferings[0]
	instanceType := best.InstanceType
	log.FromContext(ctx).Info("selected instance type", logging.InstanceType, instanceType.Name)

	// The cheapest offering determines both the capacity type (priority) and zone
	cheapest := best.Offerings.Cheapest()
	if cheapest == nil {
		return nil, "", ""
	}
	capacityType := getOfferingCapacityType(cheapest)

	// If there are multiple zones with the same cheapest price and capacity type, pick one
	priorityOfferings := lo.Filter(best.Offerings, func(o *corecloudprovider.Offering, _ int) bool {
		return getOfferingCapacityType(o) == capacityType
	})
	var zone string
	zonesWithPriority := lo.Map(priorityOfferings, func(o *corecloudprovider.Offering, _ int) string { return getOfferingZone(o) })
	if z, ok := sets.New(zonesWithPriority...).PopAny(); ok {
		zone = z
	}

	return instanceType, capacityType, zone
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
