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
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type defaultOfferingRankStage struct {
	// zoneLoad is the number of NodeClaims the NodePool already has in each zone. It is nil when the
	// load is unknown, in which case zone selection stays uniformly random.
	zoneLoad map[string]int
}

func NewDefaultOfferingRankStage(zoneLoad map[string]int) Stage {
	return &defaultOfferingRankStage{zoneLoad: zoneLoad}
}

func (s *defaultOfferingRankStage) Process(_ context.Context, instanceOfferings []InstanceOffering) []InstanceOffering {
	for idx := range instanceOfferings {
		rankOfferings(instanceOfferings[idx].Offerings, s.zoneLoad)
	}

	// Zone load intentionally does not participate here: it breaks ties between zones of a single
	// instance type, not between instance types.
	sort.Slice(instanceOfferings, func(i, j int) bool {
		comparison := compareOfferings(firstOffering(instanceOfferings[i]), firstOffering(instanceOfferings[j]))
		if comparison == 0 {
			return instanceOfferingName(instanceOfferings[i]) < instanceOfferingName(instanceOfferings[j])
		}
		return comparison < 0
	})
	return instanceOfferings
}

func rankOfferings(offerings corecloudprovider.Offerings, zoneLoad map[string]int) {
	// Shuffle before the stable sort so that offerings tied on every comparison
	// dimension (price, capacity type, placement scope, zone load) end up in random
	// order. This avoids concentrating launches in the lexically first zone when zonal
	// offerings are otherwise equivalent. Non-cryptographic randomness is
	// intentional here.
	rand.Shuffle(len(offerings), func(i, j int) { offerings[i], offerings[j] = offerings[j], offerings[i] })
	sort.SliceStable(offerings, func(i, j int) bool {
		if comparison := compareOfferings(offerings[i], offerings[j]); comparison != 0 {
			return comparison < 0
		}
		// Prefer the zone the NodePool has the fewest NodeClaims in, so launches even out over time.
		return zoneLoadOf(offerings[i], zoneLoad) < zoneLoadOf(offerings[j], zoneLoad)
	})
}

func zoneLoadOf(offering *corecloudprovider.Offering, zoneLoad map[string]int) int {
	if offering == nil || len(zoneLoad) == 0 {
		return 0
	}
	return zoneLoad[offering.Requirements.Get(corev1.LabelTopologyZone).Any()]
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
// Zones are intentionally not compared here; rankOfferings orders otherwise
// equivalent offerings by zone load, shuffling beforehand so equally loaded
// zones are picked at random.
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
	// Prefer the lower priced offering.
	if i.Price < j.Price {
		return -1
	}
	if i.Price > j.Price {
		return 1
	}
	// Preserve Karpenter's spot-before-on-demand tie-break.
	if iCapacityRank, jCapacityRank := capacityTypeRank(i), capacityTypeRank(j); iCapacityRank != jCapacityRank {
		return iCapacityRank - jCapacityRank
	}
	// Prefer zonal over regional within the same price and capacity type.
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
		// This should be unreachable for provider-generated offerings. Rank
		// malformed offerings last so they never outrank known placement scopes.
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
