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

package stages

import (
	"context"
	"math/rand"
	"sort"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type defaultOfferingRankStage struct{}

func NewDefaultOfferingRankStage() Stage {
	return &defaultOfferingRankStage{}
}

func (s *defaultOfferingRankStage) Process(_ context.Context, instanceOfferings []InstanceOffering) []InstanceOffering {
	for idx := range instanceOfferings {
		rankOfferings(instanceOfferings[idx].Offerings)
	}

	sort.Slice(instanceOfferings, func(i, j int) bool {
		comparison := compareOfferings(firstOffering(instanceOfferings[i]), firstOffering(instanceOfferings[j]))
		if comparison == 0 {
			return instanceOfferingName(instanceOfferings[i]) < instanceOfferingName(instanceOfferings[j])
		}
		return comparison < 0
	})
	return instanceOfferings
}

func rankOfferings(offerings corecloudprovider.Offerings) {
	// Shuffle before the stable sort so that offerings tied on every comparison
	// dimension (price, capacity type, placement scope) end up in random order.
	// This avoids concentrating launches in the lexically first zone when zonal
	// offerings are otherwise equivalent. Non-cryptographic randomness is
	// intentional here.
	rand.Shuffle(len(offerings), func(i, j int) { offerings[i], offerings[j] = offerings[j], offerings[i] })
	sort.SliceStable(offerings, func(i, j int) bool {
		return compareOfferings(offerings[i], offerings[j]) < 0
	})
}

func firstOffering(instanceOffering InstanceOffering) *corecloudprovider.Offering {
	if len(instanceOffering.Offerings) == 0 {
		return nil
	}
	return instanceOffering.Offerings[0]
}

// compareOfferings returns a negative value when i should sort before j. The
// default precedence order is: non-nil offerings, lowest price, capacity type
// with spot preferred over on-demand, then placement scope with zonal preferred
// over regional. Since capacity type is evaluated before placement scope,
// regional spot is preferred over zonal on-demand when their prices are equal.
// Zones are intentionally not compared here; rankOfferings shuffles before
// stable sorting so otherwise equivalent offerings are spread across zones over
// time.
func compareOfferings(i, j *corecloudprovider.Offering) int {
	if i == nil && j == nil {
		return 0
	}
	if i == nil {
		return 1
	}
	if j == nil {
		return -1
	}
	if i.Price < j.Price {
		return -1
	}
	if i.Price > j.Price {
		return 1
	}
	if iCapacityRank, jCapacityRank := capacityTypeRank(i), capacityTypeRank(j); iCapacityRank != jCapacityRank {
		return iCapacityRank - jCapacityRank
	}
	if iScopeRank, jScopeRank := placementScopeRank(i), placementScopeRank(j); iScopeRank != jScopeRank {
		return iScopeRank - jScopeRank
	}
	return 0
}

func placementScopeRank(offering *corecloudprovider.Offering) int {
	switch zones.PlacementScopeForOffering(offering) {
	case v1beta1.PlacementScopeZonal:
		return 0
	case v1beta1.PlacementScopeRegional:
		return 1
	default:
		return 2
	}
}

func capacityTypeRank(offering *corecloudprovider.Offering) int {
	switch offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any() {
	case karpv1.CapacityTypeSpot:
		return 0
	case karpv1.CapacityTypeOnDemand:
		return 1
	default:
		return 2
	}
}

func instanceOfferingName(instanceOffering InstanceOffering) string {
	if instanceOffering.InstanceType == nil {
		return ""
	}
	return instanceOffering.InstanceType.Name
}
