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
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// TestCompareOfferings_DoesNotRankEquivalentZonalOfferingsByZoneName pins a
// helper invariant: zone name is not a comparator dimension. Full rank stage
// behavior is tested through allocation strategy tests.
func TestCompareOfferings_DoesNotRankEquivalentZonalOfferingsByZoneName(t *testing.T) {
	g := NewWithT(t)
	westus1 := testOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-1")
	westus2 := testOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-2")

	g.Expect(compareOfferings(westus1, westus2)).To(Equal(0))
	g.Expect(compareOfferings(westus2, westus1)).To(Equal(0))
}

func testOffering(price float64, capacityType, zone string) *corecloudprovider.Offering {
	return &corecloudprovider.Offering{
		Price: price,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
			scheduling.NewRequirement(v1beta1.LabelPlacementScope, corev1.NodeSelectorOpIn, zones.PlacementScopeForZone(zone)),
		),
		Available: true,
	}
}
