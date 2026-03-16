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
	"math"
	"sort"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type priceSortStage struct{}

func NewPriceSortStage() Stage {
	return &priceSortStage{}
}

func (s *priceSortStage) Process(_ context.Context, instanceOfferings []InstanceOffering) []InstanceOffering {
	// Sort offerings within each instance type: by price, then spot before on-demand at equal price
	for idx := range instanceOfferings {
		sortOfferings(instanceOfferings[idx].Offerings)
	}

	// Sort instance types by cheapest offering price, then by name
	sort.Slice(instanceOfferings, func(i, j int) bool {
		iPrice := cheapestOfferingPrice(instanceOfferings[i])
		jPrice := cheapestOfferingPrice(instanceOfferings[j])
		if iPrice == jPrice {
			return instanceOfferingName(instanceOfferings[i]) < instanceOfferingName(instanceOfferings[j])
		}
		return iPrice < jPrice
	})
	return instanceOfferings
}

// sortOfferings sorts offerings by price, with spot before on-demand at equal price.
func sortOfferings(offerings corecloudprovider.Offerings) {
	sort.SliceStable(offerings, func(i, j int) bool {
		if offerings[i].Price != offerings[j].Price {
			return offerings[i].Price < offerings[j].Price
		}
		// At equal price, spot comes before on-demand
		iSpot := offerings[i].Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot)
		jSpot := offerings[j].Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot)
		if iSpot != jSpot {
			return iSpot
		}
		return false
	})
}

func cheapestOfferingPrice(instanceOffering InstanceOffering) float64 {
	if len(instanceOffering.Offerings) == 0 {
		return math.MaxFloat64
	}
	cheapest := instanceOffering.Offerings.Cheapest()
	if cheapest == nil {
		return math.MaxFloat64
	}
	return cheapest.Price
}

func instanceOfferingName(instanceOffering InstanceOffering) string {
	if instanceOffering.InstanceType == nil {
		return ""
	}
	return instanceOffering.InstanceType.Name
}
