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

	allocationstrategy "github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestPickSkuSizePriorityAndZone(t *testing.T) {
	cases := []struct {
		name                 string
		instanceOfferings    []allocationstrategy.InstanceOffering
		expectedInstanceType string
		expectedPriority     string
		expectedZone         string
	}{
		{
			name:                 "No instance offerings",
			instanceOfferings:    []allocationstrategy.InstanceOffering{},
			expectedInstanceType: "",
			expectedPriority:     "",
			expectedZone:         "",
		},
		{
			name: "Selects first (cheapest) instance offering",
			instanceOfferings: []allocationstrategy.InstanceOffering{
				{
					InstanceType: &cloudprovider.InstanceType{Name: "Standard_D2s_v3"},
					Offerings: cloudprovider.Offerings{
						newOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-2"),
					},
				},
				{
					InstanceType: &cloudprovider.InstanceType{Name: "Standard_NV16as_v4"},
					Offerings: cloudprovider.Offerings{
						newOffering(0.2, karpv1.CapacityTypeOnDemand, "westus-2"),
					},
				},
			},
			expectedInstanceType: "Standard_D2s_v3",
			expectedPriority:     karpv1.CapacityTypeOnDemand,
			expectedZone:         "westus-2",
		},
		{
			name: "Picks capacity type from cheapest offering",
			instanceOfferings: []allocationstrategy.InstanceOffering{
				{
					InstanceType: &cloudprovider.InstanceType{Name: "Standard_D2s_v3"},
					Offerings: cloudprovider.Offerings{
						newOffering(0.05, karpv1.CapacityTypeSpot, "westus-1"),
						newOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-1"),
					},
				},
			},
			expectedInstanceType: "Standard_D2s_v3",
			expectedPriority:     karpv1.CapacityTypeSpot,
			expectedZone:         "westus-1",
		},
		{
			name: "Multiple zones - should pick one of the available zones",
			instanceOfferings: []allocationstrategy.InstanceOffering{
				{
					InstanceType: &cloudprovider.InstanceType{Name: "Standard_D2s_v3"},
					Offerings: cloudprovider.Offerings{
						newOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-1"),
						newOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-2"),
					},
				},
			},
			expectedInstanceType: "Standard_D2s_v3",
			expectedPriority:     karpv1.CapacityTypeOnDemand,
			// expectedZone could be either westus-1 or westus-2
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			instanceType, priority, zone := PickSkuSizePriorityAndZone(context.TODO(), c.instanceOfferings)

			if c.expectedInstanceType == "" {
				g.Expect(instanceType).To(BeNil())
			} else {
				g.Expect(instanceType).ToNot(BeNil())
				g.Expect(instanceType.Name).To(Equal(c.expectedInstanceType))
			}

			g.Expect(priority).To(Equal(c.expectedPriority))

			if c.name == "Multiple zones - should pick one of the available zones" {
				g.Expect([]string{"westus-1", "westus-2"}).To(ContainElement(zone))
			} else {
				g.Expect(zone).To(Equal(c.expectedZone))
			}
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

func newOffering(price float64, capacityType string, zone string) *cloudprovider.Offering {
	return &cloudprovider.Offering{
		Price: price,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
		),
		Available: true,
	}
}
