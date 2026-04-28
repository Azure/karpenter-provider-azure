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

	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestCompareOfferings_DoesNotRankEquivalentZonalOfferingsByZoneName(t *testing.T) {
	westus1 := testOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-1")
	westus2 := testOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-2")

	if comparison := compareOfferings(westus1, westus2); comparison != 0 {
		t.Fatalf("expected equivalent offerings in different zones to compare equal, got %d", comparison)
	}
	if comparison := compareOfferings(westus2, westus1); comparison != 0 {
		t.Fatalf("expected equivalent offerings in different zones to compare equal, got %d", comparison)
	}
}

func testOffering(price float64, capacityType, zone string) *corecloudprovider.Offering {
	return &corecloudprovider.Offering{
		Price: price,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
		),
		Available: true,
	}
}
