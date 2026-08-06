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

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/capacityrecommendation"
)

func TestCompareRecommendationGroup(t *testing.T) {
	tests := []struct {
		name                       string
		group                      *recommendationGroup
		recommendations            []capacityrecommendation.Recommendation
		expectedLocalRanking       []groupedOffering
		expectedRecommendedRanking []groupedOffering
		expectedDifferences        []offeringDifference
	}{
		{
			name: "normalizes zones and reports rank and zone differences",
			group: &recommendationGroup{
				key:     recommendationGroupKey{capacityType: karpv1.CapacityTypeOnDemand, placementScope: v1beta1.PlacementScopeZonal},
				vmSizes: []string{"Standard_D8_v5", "Standard_D8_v6", "Standard_E8_v5"},
				vmZones: map[string]sets.Set[string]{
					"Standard_D8_v5": sets.New("3", "1", "2"),
					"Standard_D8_v6": sets.New("2", "3", "1"),
					"Standard_E8_v5": sets.New("1", "3", "2"),
				},
			},
			recommendations: []capacityrecommendation.Recommendation{
				{VMSize: "Standard_D8_v6", Zone: "3", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_D8_v6", Zone: "1", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_D8_v6", Zone: "2", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_D8_v5", Zone: "2", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_D8_v5", Zone: "1", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_E8_v5", Zone: "2", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_E8_v5", Zone: "3", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_E8_v5", Zone: "1", CapacityType: karpv1.CapacityTypeOnDemand},
			},
			expectedLocalRanking: []groupedOffering{
				{VMSize: "Standard_D8_v5", Zones: []string{"1", "2", "3"}},
				{VMSize: "Standard_D8_v6", Zones: []string{"1", "2", "3"}},
				{VMSize: "Standard_E8_v5", Zones: []string{"1", "2", "3"}},
			},
			expectedRecommendedRanking: []groupedOffering{
				{VMSize: "Standard_D8_v6", Zones: []string{"1", "2", "3"}},
				{VMSize: "Standard_D8_v5", Zones: []string{"1", "2"}},
				{VMSize: "Standard_E8_v5", Zones: []string{"1", "2", "3"}},
			},
			expectedDifferences: []offeringDifference{
				{VMSize: "Standard_D8_v5", LocalRank: 1, RecommendedRank: 2, MissingZones: []string{"3"}, AdditionalZones: []string{}},
				{VMSize: "Standard_D8_v6", LocalRank: 2, RecommendedRank: 1, MissingZones: []string{}, AdditionalZones: []string{}},
			},
		},
		{
			name: "reports no differences for matching regional ranking",
			group: &recommendationGroup{
				key:     recommendationGroupKey{capacityType: karpv1.CapacityTypeOnDemand, placementScope: v1beta1.PlacementScopeRegional},
				vmSizes: []string{"Standard_D8_v5", "Standard_D8_v6", "Standard_E8_v5"},
				vmZones: map[string]sets.Set[string]{
					"Standard_D8_v5": sets.New[string](),
					"Standard_D8_v6": sets.New[string](),
					"Standard_E8_v5": sets.New[string](),
				},
			},
			recommendations: []capacityrecommendation.Recommendation{
				{VMSize: "Standard_D8_v5", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_D8_v6", CapacityType: karpv1.CapacityTypeOnDemand},
				{VMSize: "Standard_E8_v5", CapacityType: karpv1.CapacityTypeOnDemand},
			},
			expectedLocalRanking: []groupedOffering{
				{VMSize: "Standard_D8_v5", Zones: []string{}},
				{VMSize: "Standard_D8_v6", Zones: []string{}},
				{VMSize: "Standard_E8_v5", Zones: []string{}},
			},
			expectedRecommendedRanking: []groupedOffering{
				{VMSize: "Standard_D8_v5", Zones: []string{}},
				{VMSize: "Standard_D8_v6", Zones: []string{}},
				{VMSize: "Standard_E8_v5", Zones: []string{}},
			},
			expectedDifferences: []offeringDifference{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			comparison := compareRecommendationGroup(test.group, test.recommendations)
			g.Expect(comparison.localRanking).To(Equal(test.expectedLocalRanking))
			g.Expect(comparison.recommendedRanking).To(Equal(test.expectedRecommendedRanking))
			g.Expect(comparison.differences).To(Equal(test.expectedDifferences))
		})
	}
}
