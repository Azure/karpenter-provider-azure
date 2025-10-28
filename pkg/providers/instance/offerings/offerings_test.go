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

	"github.com/stretchr/testify/assert"
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
								scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
							),
							Available: true,
						},
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
			instanceType, priority, zone := PickSkuSizePriorityAndZone(context.TODO(), c.nodeClaim, c.instanceTypes)

			if c.expectedInstanceType == "" {
				assert.Nil(t, instanceType)
			} else {
				assert.NotNil(t, instanceType)
				assert.Equal(t, c.expectedInstanceType, instanceType.Name)
			}

			assert.Equal(t, c.expectedPriority, priority)

			if c.name == "Multiple zones - should pick one of the available zones" {
				// For multiple zones, just verify a zone was selected
				assert.Contains(t, []string{"westus-1", "westus-2"}, zone)
			} else {
				assert.Equal(t, c.expectedZone, zone)
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
			priority := getPriorityForInstanceType(c.nodeClaim, c.instanceType)
			assert.Equal(t, c.expectedPriority, priority)
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
			ordered := OrderInstanceTypesByPrice(c.instanceTypes, c.requirements)
			actualOrder := make([]string, len(ordered))
			for i, it := range ordered {
				actualOrder[i] = it.Name
			}
			assert.Equal(t, c.expectedOrder, actualOrder)
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
			capacityType := getOfferingCapacityType(c.offering)
			assert.Equal(t, c.expectedCapacity, capacityType)
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
			zone := getOfferingZone(c.offering)
			assert.Equal(t, c.expectedZone, zone)
		})
	}
}

func TestGetAllSingleValuedRequirementLabels(t *testing.T) {
	cases := []struct {
		name           string
		requirements   scheduling.Requirements
		expectedLabels map[string]string
	}{
		{
			name:           "Nil instance type",
			requirements:   nil,
			expectedLabels: map[string]string{},
		},
		{
			name: "Single-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement("node.kubernetes.io/instance-type", corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedLabels: map[string]string{
				"node.kubernetes.io/instance-type": "Standard_D2s_v3",
				corev1.LabelTopologyZone:           "westus-1",
			},
		},
		{
			name: "Mixed single and multi-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement("node.kubernetes.io/instance-type", corev1.NodeSelectorOpIn, "Standard_D2s_v3"),
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedLabels: map[string]string{
				"node.kubernetes.io/instance-type": "Standard_D2s_v3",
				corev1.LabelTopologyZone:           "westus-1",
				// karpv1.CapacityTypeLabelKey should be excluded because it has multiple values
			},
		},
		{
			name: "No single-valued requirements",
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1", "westus-2"),
			),
			expectedLabels: map[string]string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			labels := GetAllSingleValuedRequirementLabels(c.requirements)
			assert.Equal(t, c.expectedLabels, labels)
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
			result := GetInstanceTypeFromVMSize(c.vmSize, possibleInstanceTypes)
			assert.Equal(t, c.expectedInstanceType, result)
		})
	}
}
