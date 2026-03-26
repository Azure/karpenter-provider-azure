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

package allocationstrategy_test

import (
	"context"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestFilterInstanceOfferings_RemovesUnavailable(t *testing.T) {
	g := NewWithT(t)
	provider := allocationstrategy.NewProvider()
	requirements := scheduling.NewRequirements(
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, "In", karpv1.CapacityTypeOnDemand),
	)

	instanceTypes := []*corecloudprovider.InstanceType{
		{
			Name: "Standard_D2s_v3",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.1, true, karpv1.CapacityTypeOnDemand),
				newOffering(0.2, false, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			Name: "Standard_F16s_v2",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.05, false, karpv1.CapacityTypeOnDemand),
			},
		},
	}

	filtered := provider.FilterInstanceOfferings(context.Background(), allocationstrategy.NewInstanceOfferings(instanceTypes), requirements)
	g.Expect(filtered).To(HaveLen(1))
	g.Expect(filtered[0].InstanceType.Name).To(Equal("Standard_D2s_v3"))
	g.Expect(filtered[0].Offerings).To(HaveLen(1))
	g.Expect(filtered[0].Offerings[0].Price).To(Equal(0.1))
}

func TestFilterInstanceOfferings_ZerothItemHasExpectedPriority(t *testing.T) {
	cases := []struct {
		name             string
		instanceTypes    []*corecloudprovider.InstanceType
		requirements     scheduling.Requirements
		expectedPriority string
	}{
		{
			name: "Default to on-demand when no spot requirement",
			instanceTypes: []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: corecloudprovider.Offerings{
						newOfferingWithZone(0.5, karpv1.CapacityTypeOnDemand, "westus-1"),
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedPriority: karpv1.CapacityTypeOnDemand,
		},
		{
			name: "Select spot when spot is requested and available",
			instanceTypes: []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: corecloudprovider.Offerings{
						newOfferingWithZone(0.1, karpv1.CapacityTypeSpot, "westus-1"),
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedPriority: karpv1.CapacityTypeSpot,
		},
		{
			name: "No results when spot requested but only available in different zone",
			instanceTypes: []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: corecloudprovider.Offerings{
						newOfferingWithZone(0.1, karpv1.CapacityTypeSpot, "westus-2"),
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedPriority: "", // empty means no results expected
		},
		{
			name: "Prefer spot when both available and spot is cheaper",
			instanceTypes: []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: corecloudprovider.Offerings{
						newOfferingWithZone(0.5, karpv1.CapacityTypeOnDemand, "westus-1"),
						newOfferingWithZone(0.1, karpv1.CapacityTypeSpot, "westus-1"),
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedPriority: karpv1.CapacityTypeSpot,
		},
		{
			name: "Prefer on-demand when both available and on-demand is cheaper",
			instanceTypes: []*corecloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: corecloudprovider.Offerings{
						newOfferingWithZone(0.1, karpv1.CapacityTypeOnDemand, "westus-1"),
						newOfferingWithZone(0.5, karpv1.CapacityTypeSpot, "westus-1"),
					},
				},
			},
			requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
				scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
			),
			expectedPriority: karpv1.CapacityTypeOnDemand,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			provider := allocationstrategy.NewProvider()

			filtered := provider.FilterInstanceOfferings(context.Background(), allocationstrategy.NewInstanceOfferings(c.instanceTypes), c.requirements)

			if c.expectedPriority == "" {
				g.Expect(filtered).To(BeEmpty())
				return
			}

			g.Expect(filtered).NotTo(BeEmpty())
			// The 0th result's cheapest offering determines the priority
			cheapest := filtered[0].Offerings.Cheapest()
			capacityType := cheapest.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
			g.Expect(capacityType).To(Equal(c.expectedPriority))
		})
	}
}

func TestFilterInstanceOfferings_Requirements_FiltersByZone(t *testing.T) {
	g := NewWithT(t)
	provider := allocationstrategy.NewProvider()
	requirements := scheduling.NewRequirements(
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
	)

	instanceTypes := []*corecloudprovider.InstanceType{
		{
			Name: "Standard_D2s_v3",
			Offerings: corecloudprovider.Offerings{
				newOfferingWithZone(0.1, karpv1.CapacityTypeOnDemand, "westus-1"),
				newOfferingWithZone(0.2, karpv1.CapacityTypeOnDemand, "westus-2"),
				newOfferingWithZone(0.15, karpv1.CapacityTypeOnDemand, "westus-3"),
			},
		},
		{
			Name: "Standard_F16s_v2",
			Offerings: corecloudprovider.Offerings{
				newOfferingWithZone(0.05, karpv1.CapacityTypeOnDemand, "westus-2"),
			},
		},
	}

	filtered := provider.FilterInstanceOfferings(context.Background(), allocationstrategy.NewInstanceOfferings(instanceTypes), requirements)
	g.Expect(filtered).To(HaveLen(1))
	g.Expect(filtered[0].InstanceType.Name).To(Equal("Standard_D2s_v3"))
	g.Expect(filtered[0].Offerings).To(HaveLen(1))
	g.Expect(filtered[0].Offerings[0].Price).To(Equal(0.1))
}

func TestFilterInstanceOfferings_OrdersByPrice(t *testing.T) {
	g := NewWithT(t)
	provider := allocationstrategy.NewProvider()
	requirements := scheduling.NewRequirements(
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, "In", karpv1.CapacityTypeOnDemand),
	)

	instanceTypes := []*corecloudprovider.InstanceType{
		{
			Name: "Standard_F16s_v2",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.5, true, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			Name: "Standard_D2s_v3",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.05, true, karpv1.CapacityTypeSpot),
			},
		},
		{
			Name: "Standard_D4s_v3",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.1, true, karpv1.CapacityTypeOnDemand),
			},
		},
		{
			Name: "Standard_D64s_v3",
			Offerings: corecloudprovider.Offerings{
				newOffering(0.1, true, karpv1.CapacityTypeOnDemand), // Same price so should order alphabetically
			},
		},
	}

	filtered := provider.FilterInstanceOfferings(context.Background(), allocationstrategy.NewInstanceOfferings(instanceTypes), requirements)
	g.Expect(filtered).To(HaveLen(3))
	g.Expect([]string{filtered[0].InstanceType.Name, filtered[1].InstanceType.Name, filtered[2].InstanceType.Name}).To(Equal([]string{"Standard_D4s_v3", "Standard_D64s_v3", "Standard_F16s_v2"}))
	g.Expect(filtered[0].Offerings).To(HaveLen(1))
	g.Expect(filtered[1].Offerings).To(HaveLen(1))
	g.Expect(filtered[2].Offerings).To(HaveLen(1))
}

func TestFilterInstanceOfferings_SpotOfferingsBeforeOnDemandAtSamePrice(t *testing.T) {
	g := NewWithT(t)
	provider := allocationstrategy.NewProvider()
	requirements := scheduling.NewRequirements(
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot),
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1", "westus-2", "westus-3"),
	)

	instanceTypes := []*corecloudprovider.InstanceType{
		{
			Name: "Standard_D2s_v3",
			Offerings: corecloudprovider.Offerings{
				newOfferingWithZone(0.1, karpv1.CapacityTypeOnDemand, "westus-1"),
				newOfferingWithZone(0.1, karpv1.CapacityTypeSpot, "westus-2"),
				newOfferingWithZone(0.1, karpv1.CapacityTypeOnDemand, "westus-3"),
				newOfferingWithZone(0.1, karpv1.CapacityTypeSpot, "westus-1"),
				newOfferingWithZone(0.1, karpv1.CapacityTypeSpot, "westus-3"),
				newOfferingWithZone(0.1, karpv1.CapacityTypeOnDemand, "westus-2"),
			},
		},
	}

	filtered := provider.FilterInstanceOfferings(context.Background(), allocationstrategy.NewInstanceOfferings(instanceTypes), requirements)
	g.Expect(filtered).To(HaveLen(1))
	g.Expect(filtered[0].Offerings).To(HaveLen(6))

	for i, offering := range filtered[0].Offerings {
		capacityType := offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
		if i < 3 {
			g.Expect(capacityType).To(Equal(karpv1.CapacityTypeSpot), "expected spot offering at index %d", i)
		} else {
			g.Expect(capacityType).To(Equal(karpv1.CapacityTypeOnDemand), "expected on-demand offering at index %d", i)
		}
	}
}

func newOfferingWithZone(price float64, capacityType string, zone string) *corecloudprovider.Offering {
	return &corecloudprovider.Offering{
		Price: price,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
		),
		Available: true,
	}
}

func newOffering(price float64, available bool, capacityType string) *corecloudprovider.Offering {
	return &corecloudprovider.Offering{
		Price: price,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, "westus-1"),
		),
		Available: available,
	}
}
