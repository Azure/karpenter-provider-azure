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
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestPickSkuSizePriorityAndZone(t *testing.T) {
	cases := []struct {
		name                 string
		instanceTypes        []*cloudprovider.InstanceType
		nodeClaim            *karpv1.NodeClaim
		expectedInstanceType string
		expectedPriority     string
		expectedZone         string
	}{
		{
			name:                 "No instance types in the list",
			instanceTypes:        []*cloudprovider.InstanceType{},
			nodeClaim:            &karpv1.NodeClaim{},
			expectedInstanceType: "",
			expectedPriority:     "",
			expectedZone:         "",
		},
		{
			name: "Selects First, Cheapest SKU",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
				{
					Name: "Standard_NV16as_v4",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim:            &karpv1.NodeClaim{},
			expectedInstanceType: "Standard_D2s_v3",
			expectedZone:         "westus-2",
			expectedPriority:     karpv1.CapacityTypeOnDemand,
		},
		{
			name: "Select spot instance when requested",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.05,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
								scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPrioritySpot),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPriorityRegular),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      karpv1.CapacityTypeLabelKey,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{karpv1.CapacityTypeSpot},
							},
						},
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"westus-1"},
							},
						},
					},
				},
			},
			expectedInstanceType: "Standard_D2s_v3",
			expectedPriority:     karpv1.CapacityTypeSpot,
			expectedZone:         "westus-1",
		},
		{
			name: "Select spot instance when requested (via legacy kubernetes.azure.com/scalesetpriority label)",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.05,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
								scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPrioritySpot),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPriorityRegular),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      v1beta1.AKSLabelScaleSetPriority,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{v1beta1.ScaleSetPrioritySpot},
							},
						},
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"westus-1"},
							},
						},
					},
				},
			},
			expectedInstanceType: "Standard_D2s_v3",
			expectedPriority:     karpv1.CapacityTypeSpot,
			expectedZone:         "westus-1",
		},
		{
			name: "Multiple zones - should pick one of the available zones",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"westus-1", "westus-2"},
							},
						},
					},
				},
			},
			expectedInstanceType: "Standard_D2s_v3",
			expectedPriority:     karpv1.CapacityTypeOnDemand,
			// expectedZone could be either westus-1 or westus-2, we just check it's not empty
		},
		{
			name: "No matching offerings should return empty",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
					},
				},
			},
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"eastus-1"}, // Different zone
							},
						},
					},
				},
			},
			expectedInstanceType: "",
			expectedPriority:     "",
			expectedZone:         "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			instanceType, priority, zone := PickSkuSizePriorityAndZone(context.TODO(), c.nodeClaim, c.instanceTypes)

			if c.expectedInstanceType == "" {
				g.Expect(instanceType).To(BeNil())
			} else {
				g.Expect(instanceType).ToNot(BeNil())
				g.Expect(instanceType.Name).To(Equal(c.expectedInstanceType))
			}

			g.Expect(priority).To(Equal(c.expectedPriority))

			if c.name == "Multiple zones - should pick one of the available zones" {
				// For multiple zones, just verify a zone was selected
				g.Expect([]string{"westus-1", "westus-2"}).To(ContainElement(zone))
			} else {
				g.Expect(zone).To(Equal(c.expectedZone))
			}
		})
	}
}

func TestGetPriorityForInstanceType(t *testing.T) {
	cases := []struct {
		name             string
		nodeClaim        *karpv1.NodeClaim
		instanceType     *cloudprovider.InstanceType
		expectedPriority string
	}{
		{
			name: "Default to on-demand when no spot requirement",
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{},
				},
			},
			instanceType: &cloudprovider.InstanceType{
				Name: "Standard_D2s_v3",
				Offerings: []*cloudprovider.Offering{
					{
						Requirements: scheduling.NewRequirements(
							scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
							scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
						),
						Available: true,
					},
				},
			},
			expectedPriority: karpv1.CapacityTypeOnDemand,
		},
		{
			name: "Select spot when spot is requested and available",
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      karpv1.CapacityTypeLabelKey,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{karpv1.CapacityTypeSpot},
							},
						},
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"westus-1"},
							},
						},
					},
				},
			},
			instanceType: &cloudprovider.InstanceType{
				Name: "Standard_D2s_v3",
				Offerings: []*cloudprovider.Offering{
					{
						Requirements: scheduling.NewRequirements(
							scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
							scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
						),
						Available: true,
					},
				},
			},
			expectedPriority: karpv1.CapacityTypeSpot,
		},
		{
			name: "Fallback to on-demand when spot requested but not available in zone",
			nodeClaim: &karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      karpv1.CapacityTypeLabelKey,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{karpv1.CapacityTypeSpot},
							},
						},
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"westus-1"},
							},
						},
					},
				},
			},
			instanceType: &cloudprovider.InstanceType{
				Name: "Standard_D2s_v3",
				Offerings: []*cloudprovider.Offering{
					{
						Requirements: scheduling.NewRequirements(
							scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
							scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"), // Different zone
						),
						Available: true,
					},
				},
			},
			expectedPriority: karpv1.CapacityTypeOnDemand,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			priority := getPriorityForInstanceType(c.nodeClaim, c.instanceType)
			g.Expect(priority).To(Equal(c.expectedPriority))
		})
	}
}

func TestOrderInstanceTypesByPrice(t *testing.T) {
	cases := []struct {
		name          string
		instanceTypes []*cloudprovider.InstanceType
		requirements  scheduling.Requirements
		expectedOrder []string
	}{
		{
			name: "Order by price ascending",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Expensive",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.5,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
							),
							Available: true,
						},
					},
				},
				{
					Name: "Cheap",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
							),
							Available: true,
						},
					},
				},
				{
					Name: "Medium",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.3,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
							),
							Available: true,
						},
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
			),
			expectedOrder: []string{"Cheap", "Medium", "Expensive"},
		},
		{
			name: "Handle instances with no compatible offerings",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Compatible",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.1,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
							),
							Available: true,
						},
					},
				},
				{
					Name: "Incompatible",
					Offerings: []*cloudprovider.Offering{
						{
							Price: 0.05,
							Requirements: scheduling.NewRequirements(
								scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
							),
							Available: true,
						},
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
			),
			expectedOrder: []string{"Compatible", "Incompatible"}, // Compatible comes first even with higher price
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			ordered := OrderInstanceTypesByPrice(c.instanceTypes, c.requirements)
			actualOrder := make([]string, len(ordered))
			for i, it := range ordered {
				actualOrder[i] = it.Name
			}
			g.Expect(actualOrder).To(Equal(c.expectedOrder))
		})
	}
}

func TestGetOfferingCapacityType(t *testing.T) {
	cases := []struct {
		name             string
		offering         *cloudprovider.Offering
		expectedCapacity string
	}{
		{
			name: "On-demand capacity type",
			offering: &cloudprovider.Offering{
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
				),
			},
			expectedCapacity: karpv1.CapacityTypeOnDemand,
		},
		{
			name: "Spot capacity type",
			offering: &cloudprovider.Offering{
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
				),
			},
			expectedCapacity: karpv1.CapacityTypeSpot,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			capacityType := getOfferingCapacityType(c.offering)
			g.Expect(capacityType).To(Equal(c.expectedCapacity))
		})
	}
}

func TestGetOfferingZone(t *testing.T) {
	cases := []struct {
		name         string
		offering     *cloudprovider.Offering
		expectedZone string
	}{
		{
			name: "westus-1 zone",
			offering: &cloudprovider.Offering{
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
				),
			},
			expectedZone: "westus-1",
		},
		{
			name: "eastus-2 zone",
			offering: &cloudprovider.Offering{
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "eastus-2"),
				),
			},
			expectedZone: "eastus-2",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			zone := getOfferingZone(c.offering)
			g.Expect(zone).To(Equal(c.expectedZone))
		})
	}
}

func TestGetInstanceTypeFromVMSize(t *testing.T) {
	possibleInstanceTypes := []*cloudprovider.InstanceType{
		{Name: "Standard_D2s_v3"},
		{Name: "Standard_D4s_v3"},
		{Name: "Standard_B1s"},
	}

	cases := []struct {
		name                 string
		vmSize               string
		expectedInstanceType *cloudprovider.InstanceType
	}{
		{
			name:                 "Find existing VM size",
			vmSize:               "Standard_D2s_v3",
			expectedInstanceType: possibleInstanceTypes[0],
		},
		{
			name:                 "Find another existing VM size",
			vmSize:               "Standard_B1s",
			expectedInstanceType: possibleInstanceTypes[2],
		},
		{
			name:                 "VM size not found",
			vmSize:               "Standard_NonExistent",
			expectedInstanceType: nil,
		},
		{
			name:                 "Empty VM size",
			vmSize:               "",
			expectedInstanceType: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result := GetInstanceTypeFromVMSize(c.vmSize, possibleInstanceTypes)
			g.Expect(result).To(Equal(c.expectedInstanceType))
		})
	}
}

// Helper to create an instance type with available offerings for PreLaunchFilter tests
func createFilterTestInstanceType(name string, offeringDefs ...offering) *cloudprovider.InstanceType {
	it := &cloudprovider.InstanceType{
		Name:     name,
		Offerings: []*cloudprovider.Offering{},
	}
	for _, o := range offeringDefs {
		it.Offerings = append(it.Offerings, &cloudprovider.Offering{
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, o.capacityType),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, o.zone),
			),
			Available: true,
		})
	}
	return it
}

func TestPreLaunchFilter_NilIsAvailable_ReturnsAll(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D2s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D4s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
	}

	// nil isAvailable means fail-open: return all
	result := PreLaunchFilter(context.Background(), instanceTypes, nil)
	g.Expect(result).To(HaveLen(2))
}

func TestPreLaunchFilter_AllAvailable_ReturnsAll(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D2s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D4s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
	}

	// All available
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return true
	})
	g.Expect(result).To(HaveLen(2))
}

func TestPreLaunchFilter_AllUnavailable_ReturnsEmpty(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D2s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D4s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
	}

	// All unavailable
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return false
	})
	g.Expect(result).To(BeEmpty())
}

func TestPreLaunchFilter_PartialUnavailable_FiltersCorrectly(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D64ads_v5", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D32ads_v5", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D8s_v3", offering{"westus-1", karpv1.CapacityTypeOnDemand}),
	}

	// D64 and D32 are quota-exhausted (marked unavailable by parallel failures), D8 is still OK
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return name == "Standard_D8s_v3"
	})
	g.Expect(result).To(HaveLen(1))
	g.Expect(result[0].Name).To(Equal("Standard_D8s_v3"))
}

func TestPreLaunchFilter_MultiZone_KeepsIfAnyZoneAvailable(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D64ads_v5",
			offering{"westus-1", karpv1.CapacityTypeOnDemand},
			offering{"westus-2", karpv1.CapacityTypeOnDemand},
			offering{"westus-3", karpv1.CapacityTypeOnDemand},
		),
	}

	// Zone 1 and 2 unavailable, zone 3 still available — keep the instance type
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return zone == "westus-3"
	})
	g.Expect(result).To(HaveLen(1))
	g.Expect(result[0].Name).To(Equal("Standard_D64ads_v5"))
}

func TestPreLaunchFilter_MultiZone_RemovesIfAllZonesUnavailable(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D64ads_v5",
			offering{"westus-1", karpv1.CapacityTypeOnDemand},
			offering{"westus-2", karpv1.CapacityTypeOnDemand},
		),
	}

	// All zones unavailable
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return false
	})
	g.Expect(result).To(BeEmpty())
}

func TestPreLaunchFilter_SpotAndOnDemand_FiltersPerCapacityType(t *testing.T) {
	g := NewWithT(t)
	instanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D64ads_v5",
			offering{"westus-1", karpv1.CapacityTypeOnDemand},
			offering{"westus-1", karpv1.CapacityTypeSpot},
		),
	}

	// On-demand unavailable, spot still available — keep the instance type
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return ct == karpv1.CapacityTypeSpot
	})
	g.Expect(result).To(HaveLen(1))
}

func TestPreLaunchFilter_EmptyInput_ReturnsEmpty(t *testing.T) {
	g := NewWithT(t)
	result := PreLaunchFilter(context.Background(), []*cloudprovider.InstanceType{}, func(name, zone, ct string) bool {
		return true
	})
	g.Expect(result).To(BeEmpty())
}

func TestPreLaunchFilter_OnlyChecksAvailableOfferings(t *testing.T) {
	g := NewWithT(t)
	// Create an instance type where one offering is already marked Available=false at scheduling time
	it := &cloudprovider.InstanceType{
		Name: "Standard_D64ads_v5",
		Offerings: []*cloudprovider.Offering{
			{
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
					scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
				),
				Available: false, // Already marked unavailable at scheduling time
			},
			{
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
					scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-2"),
				),
				Available: true,
			},
		},
	}

	// westus-2 is also now unavailable in live cache
	result := PreLaunchFilter(context.Background(), []*cloudprovider.InstanceType{it}, func(name, zone, ct string) bool {
		return false // all unavailable in live cache
	})
	// Should be filtered out because the only Available=true offering (westus-2) is now unavailable in live cache
	g.Expect(result).To(BeEmpty())
}

func TestPreLaunchFilter_LargeScale_CircuitBreaker(t *testing.T) {
	g := NewWithT(t)
	// Simulate 10 instance types, all in the same SKU family
	var instanceTypes []*cloudprovider.InstanceType
	for _, size := range []string{"D4", "D8", "D16", "D32", "D64", "D96", "E4", "E8", "E16", "E32"} {
		instanceTypes = append(instanceTypes, createFilterTestInstanceType(
			"Standard_"+size+"ads_v5",
			offering{"westus-1", karpv1.CapacityTypeOnDemand},
			offering{"westus-2", karpv1.CapacityTypeOnDemand},
		))
	}

	// Simulate: first failure marks all D-series unavailable, E-series still OK
	result := PreLaunchFilter(context.Background(), instanceTypes, func(name, zone, ct string) bool {
		return name == "Standard_E4ads_v5" || name == "Standard_E8ads_v5" || name == "Standard_E16ads_v5" || name == "Standard_E32ads_v5"
	})
	g.Expect(result).To(HaveLen(4))
	for _, it := range result {
		g.Expect(it.Name).To(HavePrefix("Standard_E"))
	}
}

// TestPreLaunchFilter_SequentialNodeClaims_ProgressiveFiltering simulates the real-world
// 5000-core prompt scale scenario:
//
// 1. Scheduler creates 78 NodeClaims for D64ads_v5 (78 × 64 = 4992 cores)
// 2. First ~7 succeed (consuming 448 of 500 vCPU quota)
// 3. 8th fails with quota error → cache updated
// 4. Remaining 70 NodeClaims call PreLaunchFilter → D64 is filtered out
// 5. Fallback to lower-weight pool's SKUs
//
// This test verifies that after the cache is updated (simulating the failure),
// subsequent calls to PreLaunchFilter correctly filter out the failed SKU.
func TestPreLaunchFilter_SequentialNodeClaims_ProgressiveFiltering(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	// High-weight pool: AMD D-series (weight=5)
	amdInstanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D64ads_v5", offering{"eastus2-1", karpv1.CapacityTypeOnDemand}, offering{"eastus2-2", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D32ads_v5", offering{"eastus2-1", karpv1.CapacityTypeOnDemand}, offering{"eastus2-2", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D16ads_v5", offering{"eastus2-1", karpv1.CapacityTypeOnDemand}, offering{"eastus2-2", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D8ads_v5", offering{"eastus2-1", karpv1.CapacityTypeOnDemand}, offering{"eastus2-2", karpv1.CapacityTypeOnDemand}),
	}

	// Low-weight pool: Intel D-series (weight=1) — the fallback
	intelInstanceTypes := []*cloudprovider.InstanceType{
		createFilterTestInstanceType("Standard_D64s_v3", offering{"eastus2-1", karpv1.CapacityTypeOnDemand}, offering{"eastus2-2", karpv1.CapacityTypeOnDemand}),
		createFilterTestInstanceType("Standard_D32s_v3", offering{"eastus2-1", karpv1.CapacityTypeOnDemand}, offering{"eastus2-2", karpv1.CapacityTypeOnDemand}),
	}

	// Simulated ICE cache state: tracks which SKUs are marked unavailable
	unavailableSKUs := map[string]bool{}

	isAvailable := func(name, zone, ct string) bool {
		return !unavailableSKUs[name]
	}

	// === Phase 1: Before any failure — all AMD SKUs available ===
	result := PreLaunchFilter(ctx, amdInstanceTypes, isAvailable)
	g.Expect(result).To(HaveLen(4), "Phase 1: all AMD SKUs should be available before any failure")

	// === Phase 2: First NodeClaim fails (D64), cache updated ===
	// Simulates: handleSKUFamilyQuotaError marks D64 unavailable
	unavailableSKUs["Standard_D64ads_v5"] = true

	result = PreLaunchFilter(ctx, amdInstanceTypes, isAvailable)
	g.Expect(result).To(HaveLen(3), "Phase 2: D64 should be filtered out after first failure")
	for _, it := range result {
		g.Expect(it.Name).NotTo(Equal("Standard_D64ads_v5"))
	}

	// === Phase 3: D32 also fails (still over quota for 32 vCPUs) ===
	unavailableSKUs["Standard_D32ads_v5"] = true

	result = PreLaunchFilter(ctx, amdInstanceTypes, isAvailable)
	g.Expect(result).To(HaveLen(2), "Phase 3: D64 and D32 should be filtered out")

	// === Phase 4: D16 and D8 also fail — entire AMD pool exhausted ===
	unavailableSKUs["Standard_D16ads_v5"] = true
	unavailableSKUs["Standard_D8ads_v5"] = true

	result = PreLaunchFilter(ctx, amdInstanceTypes, isAvailable)
	g.Expect(result).To(BeEmpty(), "Phase 4: all AMD SKUs exhausted, should be empty")

	// === Phase 5: Intel (lower-weight) pool is still available ===
	result = PreLaunchFilter(ctx, intelInstanceTypes, isAvailable)
	g.Expect(result).To(HaveLen(2), "Phase 5: Intel fallback pool should be fully available")
	for _, it := range result {
		g.Expect(it.Name).To(HavePrefix("Standard_D"), "Intel SKUs should be available")
		g.Expect(it.Name).To(HaveSuffix("s_v3"), "Intel SKUs should end with v3")
	}
}
