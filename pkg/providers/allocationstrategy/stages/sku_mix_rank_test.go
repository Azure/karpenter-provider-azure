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

package stages_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"
	. "github.com/onsi/gomega"
	"github.com/patrickmn/go-cache"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy/stages"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/capacityrecommendation"
	azurezones "github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

func TestSKUMixRankStage_EnabledModeProjectsAndLocallyRanksRecommendedOfferings(t *testing.T) {
	g := NewWithT(t)
	client, provider := newTestCapacityProvider()
	client.PostBehavior.Output.Set(skuMixResponse(
		skuMixSplit("Standard_D4s_v5", "2", armrecommender.SKUMixPlacementPriorityRegular),
		skuMixSplit("Standard_D2s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular),
	))
	input := []stages.InstanceOffering{
		newInstanceOffering("Standard_D2s_v5",
			newTestOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-1")),
		newInstanceOffering("Standard_D4s_v5",
			newTestOffering(0.2, karpv1.CapacityTypeOnDemand, "westus-1"),
			newTestOffering(0.2, karpv1.CapacityTypeOnDemand, "westus-2")),
	}

	output := stages.NewSKUMixRankStage(provider, consts.ComputeRecommendationModeEnabled).Process(context.Background(), input)
	g.Expect(instanceNames(output)).To(Equal([]string{"Standard_D2s_v5", "Standard_D4s_v5"}))
	g.Expect(client.PostBehavior.Calls()).To(Equal(1))
	g.Expect(output[1].Offerings).To(HaveLen(1))
	g.Expect(output[1].Offerings[0].Requirements.Get(corev1.LabelTopologyZone).Any()).To(Equal("westus-2"))
}

func TestSKUMixRankStage_SplitsAndJoinsRecommendationGroupsPreservingSizeAndPriority(t *testing.T) {
	g := NewWithT(t)
	client, provider := newTestCapacityProvider()
	client.PostBehavior.SetCustomTransformer(func(input *fake.SKUMixPlacementScoresPostInput) error {
		priority := *input.Request.CapacityProfile.Priority
		var splits []*armrecommender.SKUMixPlacementItem
		switch {
		case priority == armrecommender.SKUMixPlacementPrioritySpot && len(input.Request.Zones) > 0:
			// Spot capacity is available only for size B in each requested zone.
			splits = []*armrecommender.SKUMixPlacementItem{
				skuMixSplit("Standard_B", "1", priority),
				skuMixSplit("Standard_B", "2", priority),
				skuMixSplit("Standard_B", "3", priority),
			}
		case priority == armrecommender.SKUMixPlacementPrioritySpot:
			splits = []*armrecommender.SKUMixPlacementItem{skuMixSplit("Standard_B", "", priority)}
		case len(input.Request.Zones) > 0:
			// Both sizes have dedicated capacity, with the original size A priority retained.
			splits = []*armrecommender.SKUMixPlacementItem{
				skuMixSplit("Standard_A", "1", priority),
				skuMixSplit("Standard_A", "2", priority),
				skuMixSplit("Standard_A", "3", priority),
				skuMixSplit("Standard_B", "1", priority),
				skuMixSplit("Standard_B", "2", priority),
				skuMixSplit("Standard_B", "3", priority),
			}
		default:
			splits = []*armrecommender.SKUMixPlacementItem{
				skuMixSplit("Standard_A", "", priority),
				skuMixSplit("Standard_B", "", priority),
			}
		}
		client.PostBehavior.Output.Set(skuMixResponse(splits...))
		return nil
	})
	input := []stages.InstanceOffering{
		newInstanceOffering("Standard_A",
			newTestOffering(0.01, karpv1.CapacityTypeSpot, "westus-1"),
			newTestOffering(0.01, karpv1.CapacityTypeSpot, "westus-2"),
			newTestOffering(0.01, karpv1.CapacityTypeSpot, "westus-3"),
			newTestOffering(0.01, karpv1.CapacityTypeSpot, azurezones.Regional),
			newTestOffering(0.03, karpv1.CapacityTypeOnDemand, "westus-1"),
			newTestOffering(0.03, karpv1.CapacityTypeOnDemand, "westus-2"),
			newTestOffering(0.03, karpv1.CapacityTypeOnDemand, "westus-3"),
			newTestOffering(0.03, karpv1.CapacityTypeOnDemand, azurezones.Regional)),
		newInstanceOffering("Standard_B",
			newTestOffering(0.05, karpv1.CapacityTypeSpot, "westus-1"),
			newTestOffering(0.05, karpv1.CapacityTypeSpot, "westus-2"),
			newTestOffering(0.05, karpv1.CapacityTypeSpot, "westus-3"),
			newTestOffering(0.05, karpv1.CapacityTypeSpot, azurezones.Regional),
			newTestOffering(0.07, karpv1.CapacityTypeOnDemand, "westus-1"),
			newTestOffering(0.07, karpv1.CapacityTypeOnDemand, "westus-2"),
			newTestOffering(0.07, karpv1.CapacityTypeOnDemand, "westus-3"),
			newTestOffering(0.07, karpv1.CapacityTypeOnDemand, azurezones.Regional)),
	}

	output := stages.NewSKUMixRankStage(provider, consts.ComputeRecommendationModeEnabled).Process(context.Background(), input)
	g.Expect(client.PostBehavior.Calls()).To(Equal(4))
	// Confirm we sent 4 groups, spot and on-demand, each with and without zones.
	requests := map[string]armrecommender.SKUMixPlacementRequest{}
	for range 4 {
		request := client.PostBehavior.CalledWithInput.Pop().Request
		scope := v1beta1.PlacementScopeRegional
		if len(request.Zones) > 0 {
			scope = v1beta1.PlacementScopeZonal
		}
		requests[fmt.Sprintf("%s|%s", *request.CapacityProfile.Priority, scope)] = request
	}
	g.Expect(requests).To(HaveLen(4))
	for _, request := range requests {
		g.Expect(requestVMSizeNames(request)).To(Equal([]string{"Standard_A", "Standard_B"}))
		g.Expect(*request.CapacityProfile.OSType).To(Equal(armrecommender.SKUMixPlacementOSTypeLinux))
		g.Expect(*request.CapacityProfile.Capacity).To(Equal(int32(10)))
	}
	g.Expect(requests[fmt.Sprintf("%s|%s", armrecommender.SKUMixPlacementPriorityRegular, v1beta1.PlacementScopeRegional)].Zones).To(BeEmpty())
	g.Expect(requests[fmt.Sprintf("%s|%s", armrecommender.SKUMixPlacementPriorityRegular, v1beta1.PlacementScopeZonal)].Zones).To(ConsistOf(to.Ptr("1"), to.Ptr("2"), to.Ptr("3")))
	g.Expect(requests[fmt.Sprintf("%s|%s", armrecommender.SKUMixPlacementPrioritySpot, v1beta1.PlacementScopeRegional)].Zones).To(BeEmpty())
	g.Expect(requests[fmt.Sprintf("%s|%s", armrecommender.SKUMixPlacementPrioritySpot, v1beta1.PlacementScopeZonal)].Zones).To(ConsistOf(to.Ptr("1"), to.Ptr("2"), to.Ptr("3")))

	order := offeringOrder(output)
	g.Expect(order).To(HaveLen(12))
	// order within this may vary due to expected zonal shuffling, so we use ConsistOf
	g.Expect(order[0:3]).To(ConsistOf(
		"Standard_A|on-demand|westus-1",
		"Standard_A|on-demand|westus-2",
		"Standard_A|on-demand|westus-3",
	))
	g.Expect(order[3]).To(Equal("Standard_A|on-demand|regional"))
	// order within this may vary due to expected zonal shuffling, so we use ConsistOf
	g.Expect(order[4:7]).To(ConsistOf(
		"Standard_B|spot|westus-1",
		"Standard_B|spot|westus-2",
		"Standard_B|spot|westus-3",
	))
	g.Expect(order[7]).To(Equal("Standard_B|spot|regional"))
	// order within this may vary due to expected zonal shuffling, so we use ConsistOf
	g.Expect(order[8:11]).To(ConsistOf(
		"Standard_B|on-demand|westus-1",
		"Standard_B|on-demand|westus-2",
		"Standard_B|on-demand|westus-3",
	))
	g.Expect(order[11]).To(Equal("Standard_B|on-demand|regional"))
}

func TestSKUMixRankStage_FailsOpenOnProviderError(t *testing.T) {
	g := NewWithT(t)
	client, provider := newTestCapacityProvider()
	client.PostBehavior.Error.Set(errors.New("API unavailable"))
	input := []stages.InstanceOffering{
		newInstanceOffering("Standard_D2s_v5",
			newTestOffering(0.1, karpv1.CapacityTypeOnDemand, "westus-1")),
		newInstanceOffering("Standard_D4s_v5",
			newTestOffering(0.2, karpv1.CapacityTypeOnDemand, "westus-1")),
	}

	output := stages.NewSKUMixRankStage(provider, consts.ComputeRecommendationModeEnabled).Process(context.Background(), input)
	g.Expect(instanceNames(output)).To(Equal([]string{"Standard_D2s_v5", "Standard_D4s_v5"}))
}

func newTestCapacityProvider() (*fake.SKUMixPlacementScoresAPI, capacityrecommendation.Provider) {
	client := &fake.SKUMixPlacementScoresAPI{}
	provider := capacityrecommendation.NewProvider(client, cache.New(cache.NoExpiration, time.Minute), "eastus")
	return client, provider
}

func skuMixResponse(splits ...*armrecommender.SKUMixPlacementItem) *armrecommender.SKUMixPlacementScoresClientPostResponse {
	return &armrecommender.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: armrecommender.SKUMixPlacementResponse{
			ValidUntil: to.Ptr(time.Now().Add(2 * time.Minute)),
			PlacementChoices: []*armrecommender.SKUMixPlacementDeploymentChoice{
				{
					ID:       to.Ptr("recommendation-1"),
					Score:    to.Ptr(int32(9)),
					SKUSplit: splits,
				},
			},
		},
	}
}

func skuMixSplit(vmSize, zone string, priority armrecommender.SKUMixPlacementPriority) *armrecommender.SKUMixPlacementItem {
	split := &armrecommender.SKUMixPlacementItem{
		Name:     to.Ptr(vmSize),
		Priority: to.Ptr(priority),
		Capacity: to.Ptr(int32(1)),
	}
	if zone != "" {
		split.Zone = to.Ptr(zone)
	}
	return split
}

func requestVMSizeNames(request armrecommender.SKUMixPlacementRequest) []string {
	result := make([]string, 0, len(request.InstanceDescription.VMSizes))
	for _, vmSize := range request.InstanceDescription.VMSizes {
		result = append(result, *vmSize.Name)
	}
	return result
}

func newInstanceOffering(name string, offerings ...*corecloudprovider.Offering) stages.InstanceOffering {
	return stages.InstanceOffering{
		InstanceType: &corecloudprovider.InstanceType{
			Name: name,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Linux)),
			),
		},
		Offerings: offerings,
	}
}

func newTestOffering(price float64, capacityType, zone string) *corecloudprovider.Offering {
	return &corecloudprovider.Offering{
		Price: price,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityType),
			scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zone),
			scheduling.NewRequirement(v1beta1.LabelPlacementScope, corev1.NodeSelectorOpIn, azurezones.PlacementScopeForZone(zone)),
		),
		Available: true,
	}
}

func instanceNames(instanceOfferings []stages.InstanceOffering) []string {
	names := make([]string, 0, len(instanceOfferings))
	for _, instanceOffering := range instanceOfferings {
		names = append(names, instanceOffering.InstanceType.Name)
	}
	return names
}

func offeringOrder(instanceOfferings []stages.InstanceOffering) []string {
	var result []string
	for _, instanceOffering := range instanceOfferings {
		for _, offering := range instanceOffering.Offerings {
			zone := offering.Requirements.Get(corev1.LabelTopologyZone).Any()
			if azurezones.PlacementScopeForOffering(offering) == v1beta1.PlacementScopeRegional {
				zone = "regional"
			}
			result = append(result, fmt.Sprintf("%s|%s|%s",
				instanceOffering.InstanceType.Name,
				offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any(),
				zone,
			))
		}
	}
	return result
}
