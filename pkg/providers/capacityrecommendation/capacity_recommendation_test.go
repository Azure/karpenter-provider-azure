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

package capacityrecommendation_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azcorefake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armrecommender "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"
	. "github.com/onsi/gomega"
	"github.com/patrickmn/go-cache"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/capacityrecommendation"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSKUMixPlacementScoresClientPost_DecodesCapacityLimits(t *testing.T) {
	g := NewWithT(t)
	const responseBody = `{
		"capacityLimits":[{"limit":5,"name":"Standard_D4s_v5","priority":"Spot","reason":"None","zone":"1"}],
		"id":"f5d3a7cf-6b12-418b-a8f7-aebd6ff219f8",
		"partialFulfillmentReason":"None",
		"placementChoices":[{"id":"choice-id","score":9,"skuSplit":[{"capacity":2,"name":"Standard_D4s_v5","priority":"Spot","zone":"1"}]}],
		"validUntil":"2026-08-28T20:14:47.9763294+00:00"
	}`
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		g.Expect(request.Method).To(Equal(http.MethodPost))
		g.Expect(request.URL.Path).To(Equal("/subscriptions/subscription-id/providers/Microsoft.Compute/locations/eastus/skuMixPlacementScores/recommendations/generate"))
		g.Expect(request.URL.Query().Get("api-version")).To(Equal("2026-05-05-preview"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Request:    request,
		}, nil
	})
	client, err := capacityrecommendation.NewSKUMixPlacementScoresClient(
		"subscription-id",
		&azcorefake.TokenCredential{},
		&arm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: transport}},
	)
	g.Expect(err).NotTo(HaveOccurred())

	response, err := client.Post(context.Background(), "eastus", armrecommender.SKUMixPlacementRequest{}, nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(response.CapacityLimits).To(HaveLen(1))
	g.Expect(response.CapacityLimits[0]).To(Equal(&capacityrecommendation.SKUMixPlacementCapacityLimit{
		Limit:    to.Ptr(int32(5)),
		Name:     to.Ptr("Standard_D4s_v5"),
		Priority: to.Ptr(armrecommender.SKUMixPlacementPrioritySpot),
		Reason:   to.Ptr("None"),
		Zone:     to.Ptr("1"),
	}))
	g.Expect(response.ID).NotTo(BeNil())
	g.Expect(*response.ID).To(Equal("f5d3a7cf-6b12-418b-a8f7-aebd6ff219f8"))
	g.Expect(response.PlacementChoices).To(HaveLen(1))
	g.Expect(response.ValidUntil).NotTo(BeNil())
	g.Expect(*response.ValidUntil).To(BeTemporally("==", time.Date(2026, 8, 28, 20, 14, 47, 976329400, time.UTC)))
}

func TestGetRecommendations_ReturnsRecommendations(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(recommendationResponse(time.Now().Add(time.Minute), 9, "Standard_D4s_v5", "2"))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(
		context.Background(),
		&capacityrecommendation.RankingInput{
			VMSizes:      []string{"Standard_D2s_v5", "Standard_D4s_v5"},
			Zones:        []string{"1", "2"},
			CapacityType: karpv1.CapacityTypeOnDemand,
			OSType:       corev1.Linux,
			Count:        5,
		})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{{
		VMSize: "Standard_D4s_v5", Zone: "2", CapacityType: karpv1.CapacityTypeOnDemand,
		Score: 9, ID: "choice-id", Count: 5,
	}}))
	g.Expect(client.PostBehavior.Calls()).To(Equal(1))

	input := client.PostBehavior.CalledWithInput.Pop()
	g.Expect(input.Location).To(Equal("eastus"))
	g.Expect(*input.Request.CapacityProfile.Capacity).To(Equal(int32(5)))
	g.Expect(*input.Request.CapacityProfile.CapacityType).To(Equal(armrecommender.SKUMixPlacementCapacityTypeVM))
	g.Expect(*input.Request.CapacityProfile.Priority).To(Equal(armrecommender.SKUMixPlacementPriorityRegular))
	g.Expect(*input.Request.CapacityProfile.AllocationStrategy).To(Equal(armrecommender.SKUMixPlacementAllocationStrategyPrioritized))
	g.Expect(*input.Request.CapacityProfile.OSType).To(Equal(armrecommender.SKUMixPlacementOSTypeLinux))
	g.Expect(input.Request.Zones).To(ConsistOf(to.Ptr("1"), to.Ptr("2")))
	g.Expect(input.Request.InstanceDescription.VMSizes).To(HaveLen(2))
	g.Expect(*input.Request.InstanceDescription.VMSizes[0].Name).To(Equal("Standard_D2s_v5"))
	g.Expect(*input.Request.InstanceDescription.VMSizes[0].Rank).To(Equal(int32(0)))
}

func TestGetRecommendations_CacheIgnoresZoneOrderAndCount(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(recommendationResponse(time.Now().Add(time.Minute), 8, "Standard_D2s_v5", "1"))
	cache := newCache()
	provider := capacityrecommendation.NewProvider(client, cache, "eastus")

	first, err := provider.GetRecommendations(
		context.Background(),
		&capacityrecommendation.RankingInput{
			VMSizes:      []string{"Standard_D2s_v5", "Standard_D4s_v5"},
			Zones:        []string{"1", "2"},
			CapacityType: karpv1.CapacityTypeOnDemand,
			OSType:       corev1.Linux,
			Count:        5,
		})
	g.Expect(err).NotTo(HaveOccurred())

	// mutate the ID so we can assert the second call doesn't see the mutated ID
	first[0].ID = "caller-mutated"
	second, err := provider.GetRecommendations(
		context.Background(),
		&capacityrecommendation.RankingInput{
			VMSizes:      []string{"Standard_D2s_v5", "Standard_D4s_v5"},
			Zones:        []string{"2", "1"},
			CapacityType: karpv1.CapacityTypeOnDemand,
			OSType:       corev1.Linux,
			Count:        1,
		})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(second[0].ID).To(Equal("choice-id"))
	g.Expect(client.PostBehavior.Calls()).To(Equal(1))
	g.Expect(cache.Items()).To(HaveLen(1))
	for _, item := range cache.Items() {
		cached, ok := item.Object.([]capacityrecommendation.Recommendation)
		g.Expect(ok).To(BeTrue())
		g.Expect(cached).To(Equal(second))
	}
}

func TestGetRecommendations_CacheKeyIncludesVMSizeOrder(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(recommendationResponse(time.Now().Add(time.Minute), 8, "Standard_D2s_v5", "1"))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	input := capacityRecommendationInput()
	input.VMSizes = []string{"Standard_D2s_v5", "Standard_D4s_v5"}
	_, err := provider.GetRecommendations(context.Background(), input)
	g.Expect(err).NotTo(HaveOccurred())

	input.VMSizes = []string{"Standard_D4s_v5", "Standard_D2s_v5"}
	_, err = provider.GetRecommendations(context.Background(), input)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(client.PostBehavior.Calls()).To(Equal(2))
}

func TestGetRecommendations_CacheKeyIncludesOSType(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(recommendationResponse(time.Now().Add(time.Minute), 9, "Standard_D2s_v5", "1"))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	linuxInput := capacityRecommendationInput()
	_, err := provider.GetRecommendations(context.Background(), linuxInput)
	g.Expect(err).NotTo(HaveOccurred())

	windowsInput := capacityRecommendationInput()
	windowsInput.OSType = corev1.Windows
	_, err = provider.GetRecommendations(context.Background(), windowsInput)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(client.PostBehavior.Calls()).To(Equal(2))
}

func TestGetRecommendations_APIErrorIsPropagated(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Error.Set(errors.New("recommendation API unavailable"))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).To(MatchError("recommendation API unavailable"))
	g.Expect(recommendations).To(BeNil())
	g.Expect(client.PostBehavior.Calls()).To(Equal(1))
}

func TestGetRecommendations_InvalidInputErrorIsReturned(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(
		context.Background(),
		&capacityrecommendation.RankingInput{
			CapacityType: karpv1.CapacityTypeOnDemand,
			Count:        5,
		})
	g.Expect(recommendations).To(BeNil())
	g.Expect(err).To(MatchError(ContainSubstring("no VM sizes specified")))
	g.Expect(client.PostBehavior.Calls()).To(Equal(0))
}

func TestGetRecommendations_InvalidResponseErrorIsReturned(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(&capacityrecommendation.SKUMixPlacementScoresClientPostResponse{})
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).To(MatchError(ContainSubstring("no placement choices")))
	g.Expect(recommendations).To(BeNil())
}

func TestGetRecommendations_ExpandsOneChoiceIntoSizeZoneEntries(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(
		placementResponse(
			placementChoice("123", 9,
				placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 2),
				placementSplit("Standard_D8s_v5", "2", armrecommender.SKUMixPlacementPriorityRegular, 2),
				placementSplit("Standard_D8s_v5", "3", armrecommender.SKUMixPlacementPriorityRegular, 1),
			),
		),
	)
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "123", 9, 2),
		recommendation("Standard_D8s_v5", "2", "123", 9, 2),
		recommendation("Standard_D8s_v5", "3", "123", 9, 1),
	}))
}

func TestGetRecommendations_CombinesUniqueEntriesFromChoices(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(
		placementResponse(
			placementChoice("456", 9,
				placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 1),
				placementSplit("Standard_D8s_v5", "2", armrecommender.SKUMixPlacementPriorityRegular, 1),
				placementSplit("Standard_E8s_v5", "3", armrecommender.SKUMixPlacementPriorityRegular, 1),
			),
			placementChoice("123", 9,
				placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 2),
				placementSplit("Standard_D8s_v5", "2", armrecommender.SKUMixPlacementPriorityRegular, 2),
				placementSplit("Standard_D8s_v6", "3", armrecommender.SKUMixPlacementPriorityRegular, 1),
			),
		),
	)
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")
	input := capacityRecommendationInput()
	input.VMSizes = []string{"Standard_D8s_v5", "Standard_D8s_v6", "Standard_E8s_v5"}
	input.Zones = []string{"1", "2", "3"}

	recommendations, err := provider.GetRecommendations(context.Background(), input)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "123", 9, 2),
		recommendation("Standard_D8s_v5", "2", "123", 9, 2),
		recommendation("Standard_D8s_v6", "3", "123", 9, 1),
		recommendation("Standard_E8s_v5", "3", "456", 9, 1),
	}))
}

func TestGetRecommendations_KeepsHighestScoringEntryForDuplicateKey(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(
		placementResponse(
			placementChoice("lower", 4,
				placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 5),
			),
			placementChoice("higher", 9,
				placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 2),
			),
		),
	)
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "higher", 9, 2),
	}))
}

func TestGetRecommendations_TreatsCapacityTypeAsPartOfRecommendationIdentity(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(placementResponse(
		placementChoice("choice", 9,
			// Note: It is not actually possible to get a regular + spot in the same response right now, but
			// we're just testing here to ensure that if/when that is supported we can handle it correctly.
			placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 1),
			placementSplit("Standard_D8s_v5", "1", armrecommender.SKUMixPlacementPrioritySpot, 2),
		),
	))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "choice", 9, 1),
		{VMSize: "Standard_D8s_v5", Zone: "1", CapacityType: karpv1.CapacityTypeSpot, Score: 9, ID: "choice", Count: 2},
	}))
}

func TestGetRecommendations_PreservesAPIReturnOrderForCompleteChoiceTie(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(placementResponse(
		placementChoice("first", 9,
			placementSplit("Standard_D2s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 1),
		),
		placementChoice("second", 9,
			placementSplit("Standard_D2s_v5", "1", armrecommender.SKUMixPlacementPriorityRegular, 1),
		),
	))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D2s_v5", "1", "first", 9, 1),
	}))
}

func TestGetRecommendations_ReturnsErrorIfSplitAPIMissingPriorityInResponse(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(placementResponse(
		placementChoice("choice", 9, &armrecommender.SKUMixPlacementItem{
			Name: to.Ptr("Standard_D2s_v5"), Zone: to.Ptr("1"), Capacity: to.Ptr(int32(1)),
		}),
	))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).To(MatchError(ContainSubstring("invalid SKU split")))
	g.Expect(recommendations).To(BeNil())
}

func TestGetRecommendations_OrdersHighestScoringPlacementChoiceFirstWhenAPIReturnsOutOfOrder(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(withRegularPriority(&capacityrecommendation.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: capacityrecommendation.SKUMixPlacementResponse{
			ValidUntil: to.Ptr(time.Now().Add(time.Minute)),
			PlacementChoices: []*armrecommender.SKUMixPlacementDeploymentChoice{
				{
					ID:    to.Ptr("first-choice"),
					Score: to.Ptr(int32(4)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D2s_v5"), Capacity: to.Ptr(int32(5)), Zone: to.Ptr("1")},
					},
				},
				// Second in order (bug in recommendation API) but higher score, should be selected
				{
					ID:    to.Ptr("selected-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D4s_v5"), Capacity: to.Ptr(int32(3)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("3")},
					},
				},
			},
		},
	}))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D4s_v5", "2", "selected-choice", 9, 3),
		recommendation("Standard_D8s_v5", "3", "selected-choice", 9, 2),
		recommendation("Standard_D2s_v5", "1", "first-choice", 4, 5),
	}))
}

func TestGetRecommendations_BreaksScoreTieUsingRequestedSKUOrder(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(withRegularPriority(&capacityrecommendation.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: capacityrecommendation.SKUMixPlacementResponse{
			ValidUntil: to.Ptr(time.Now().Add(time.Minute)),
			PlacementChoices: []*armrecommender.SKUMixPlacementDeploymentChoice{
				{
					ID:    to.Ptr("d2-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D2s_v3"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D2s_v3"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_D2s_v3"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("3")},
					},
				},
				{
					ID:    to.Ptr("d8-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("3")},
					},
				},
				{
					ID:    to.Ptr("mixed-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(3)), Zone: to.Ptr("3")},
					},
				},
			},
		},
	}))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")
	input := capacityRecommendationInput()
	input.VMSizes = []string{"Standard_D8s_v5", "Standard_D2s_v3", "Standard_E2s_v3"}
	input.Zones = []string{"1", "2", "3"}

	recommendations, err := provider.GetRecommendations(context.Background(), input)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "d8-choice", 9, 2),
		recommendation("Standard_D8s_v5", "2", "d8-choice", 9, 2),
		recommendation("Standard_D8s_v5", "3", "d8-choice", 9, 1),
		recommendation("Standard_E2s_v3", "3", "mixed-choice", 9, 3),
		recommendation("Standard_D2s_v3", "1", "d2-choice", 9, 2),
		recommendation("Standard_D2s_v3", "2", "d2-choice", 9, 2),
		recommendation("Standard_D2s_v3", "3", "d2-choice", 9, 1),
	}))
}

func TestGetRecommendations_BreaksScoreAndSKUTieUsingRequestedZoneCoverage(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(withRegularPriority(&capacityrecommendation.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: capacityrecommendation.SKUMixPlacementResponse{
			ValidUntil: to.Ptr(time.Now().Add(time.Minute)),
			PlacementChoices: []*armrecommender.SKUMixPlacementDeploymentChoice{
				{
					ID:    to.Ptr("first-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("3")},
					},
				},
				{
					ID:    to.Ptr("selected-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(2)), Zone: to.Ptr("3")},
					},
				},
			},
		},
	}))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")
	input := capacityRecommendationInput()
	input.VMSizes = []string{"Standard_D8s_v5", "Standard_D2s_v3", "Standard_E2s_v3"}
	input.Zones = []string{"1", "2", "3"}

	recommendations, err := provider.GetRecommendations(context.Background(), input)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "selected-choice", 9, 2),
		recommendation("Standard_D8s_v5", "2", "selected-choice", 9, 1),
		recommendation("Standard_E2s_v3", "3", "selected-choice", 9, 2),
		recommendation("Standard_E2s_v3", "1", "first-choice", 9, 2),
	}))
}

func TestGetRecommendations_UsesOverallZoneCoverageAfterPerSKUTie(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(withRegularPriority(&capacityrecommendation.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: capacityrecommendation.SKUMixPlacementResponse{
			ValidUntil: to.Ptr(time.Now().Add(time.Minute)),
			PlacementChoices: []*armrecommender.SKUMixPlacementDeploymentChoice{
				{
					ID:    to.Ptr("two-zone-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("2")},
					},
				},
				{
					ID:    to.Ptr("three-zone-choice"),
					Score: to.Ptr(int32(9)),
					SKUSplit: []*armrecommender.SKUMixPlacementItem{
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("1")},
						{Name: to.Ptr("Standard_D8s_v5"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("2")},
						{Name: to.Ptr("Standard_E2s_v3"), Capacity: to.Ptr(int32(1)), Zone: to.Ptr("3")},
					},
				},
			},
		},
	}))
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")
	input := capacityRecommendationInput()
	input.VMSizes = []string{"Standard_D8s_v5", "Standard_E2s_v3"}
	input.Zones = []string{"1", "2", "3"}
	input.Count = 4

	recommendations, err := provider.GetRecommendations(context.Background(), input)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(recommendations).To(Equal([]capacityrecommendation.Recommendation{
		recommendation("Standard_D8s_v5", "1", "three-zone-choice", 9, 1),
		recommendation("Standard_D8s_v5", "2", "three-zone-choice", 9, 1),
		recommendation("Standard_E2s_v3", "2", "three-zone-choice", 9, 1),
		recommendation("Standard_E2s_v3", "3", "three-zone-choice", 9, 1),
		recommendation("Standard_E2s_v3", "1", "two-zone-choice", 9, 1),
	}))
}

func TestGetRecommendations_HonorsValidUntil(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	cache := newCache()
	validUntil := time.Now().Add(time.Minute)
	client.PostBehavior.Output.Set(recommendationResponse(validUntil, 9, "Standard_D2s_v5", "1"))
	provider := capacityrecommendation.NewProvider(client, cache, "eastus")

	_, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())

	items := cache.Items()
	g.Expect(items).To(HaveLen(1))
	for _, item := range items {
		g.Expect(time.Unix(0, item.Expiration)).To(BeTemporally("~", validUntil, time.Millisecond))
	}
}

func TestGetRecommendations_UsesDefaultTTLWhenValidUntilIsShort(t *testing.T) {
	g := NewWithT(t)
	const expectedDefaultTTL = 45 * time.Second
	client := &fake.SKUMixPlacementScoresAPI{}
	cache := newCache()
	validUntil := time.Now().Add(time.Second)
	client.PostBehavior.Output.Set(recommendationResponse(validUntil, 9, "Standard_D2s_v5", "1"))
	provider := capacityrecommendation.NewProvider(client, cache, "eastus")
	startedAt := time.Now()

	_, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
	g.Expect(err).NotTo(HaveOccurred())
	completedAt := time.Now()

	items := cache.Items()
	g.Expect(items).To(HaveLen(1))
	for _, item := range items {
		expiresAt := time.Unix(0, item.Expiration)
		g.Expect(expiresAt).To(BeTemporally(">=", startedAt.Add(expectedDefaultTTL)))
		g.Expect(expiresAt).To(BeTemporally("<=", completedAt.Add(expectedDefaultTTL)))
		g.Expect(expiresAt).To(BeTemporally(">", validUntil))
	}
}

func TestGetRecommendations_DeduplicatesConcurrentRequests(t *testing.T) {
	g := NewWithT(t)
	client := &fake.SKUMixPlacementScoresAPI{}
	client.PostBehavior.Output.Set(recommendationResponse(time.Now().Add(time.Minute), 9, "Standard_D2s_v5", "1"))
	started := make(chan struct{})
	release := make(chan struct{})
	client.PostBehavior.SetCustomTransformer(func(*fake.SKUMixPlacementScoresPostInput) error {
		close(started)
		<-release
		return nil
	})
	provider := capacityrecommendation.NewProvider(client, newCache(), "eastus")

	var wg sync.WaitGroup
	type result struct {
		recommendations []capacityrecommendation.Recommendation
		err             error
	}
	results := make(chan result, 6)
	request := func() {
		defer wg.Done()
		recommendations, err := provider.GetRecommendations(context.Background(), capacityRecommendationInput())
		results <- result{recommendations: recommendations, err: err}
	}

	wg.Add(1)
	go request()
	<-started
	for range 5 {
		wg.Add(1)
		go request()
	}
	close(release)
	wg.Wait()
	close(results)

	for result := range results {
		g.Expect(result.err).NotTo(HaveOccurred())
		g.Expect(result.recommendations).To(HaveLen(1))
		g.Expect(result.recommendations[0].ID).To(Equal("choice-id"))
	}
	g.Expect(client.PostBehavior.Calls()).To(Equal(1))
}

func capacityRecommendationInput() *capacityrecommendation.RankingInput {
	return &capacityrecommendation.RankingInput{
		VMSizes:      []string{"Standard_D2s_v5"},
		Zones:        []string{"1"},
		CapacityType: karpv1.CapacityTypeOnDemand,
		OSType:       corev1.Linux,
		Count:        5,
	}
}

func newCache() *cache.Cache {
	return cache.New(cache.NoExpiration, time.Minute)
}

func recommendation(vmSize, zone, id string, score, count int32) capacityrecommendation.Recommendation {
	return capacityrecommendation.Recommendation{
		VMSize: vmSize, Zone: zone, CapacityType: karpv1.CapacityTypeOnDemand,
		Score: score, ID: id, Count: count,
	}
}

func placementSplit(name, zone string, priority armrecommender.SKUMixPlacementPriority, count int32) *armrecommender.SKUMixPlacementItem {
	return &armrecommender.SKUMixPlacementItem{
		Name: to.Ptr(name), Zone: to.Ptr(zone), Priority: to.Ptr(priority), Capacity: to.Ptr(count),
	}
}

func placementChoice(id string, score int32, splits ...*armrecommender.SKUMixPlacementItem) *armrecommender.SKUMixPlacementDeploymentChoice {
	return &armrecommender.SKUMixPlacementDeploymentChoice{
		ID:       to.Ptr(id),
		Score:    to.Ptr(score),
		SKUSplit: splits,
	}
}

func placementResponse(choices ...*armrecommender.SKUMixPlacementDeploymentChoice) *capacityrecommendation.SKUMixPlacementScoresClientPostResponse {
	return &capacityrecommendation.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: capacityrecommendation.SKUMixPlacementResponse{
			ValidUntil:       to.Ptr(time.Now().Add(time.Minute)),
			PlacementChoices: choices,
		},
	}
}

func withRegularPriority(response *capacityrecommendation.SKUMixPlacementScoresClientPostResponse) *capacityrecommendation.SKUMixPlacementScoresClientPostResponse {
	for _, choice := range response.PlacementChoices {
		for _, split := range choice.SKUSplit {
			if split.Priority == nil {
				split.Priority = to.Ptr(armrecommender.SKUMixPlacementPriorityRegular)
			}
		}
	}
	return response
}

func recommendationResponse(validUntil time.Time, score int32, name string, zone string) *capacityrecommendation.SKUMixPlacementScoresClientPostResponse {
	response := recommendationResponseWithSplits(validUntil, "choice-id",
		armrecommender.SKUMixPlacementItem{
			Name:     to.Ptr(name),
			Capacity: to.Ptr(int32(5)),
			Zone:     to.Ptr(zone),
		},
	)
	response.PlacementChoices[0].Score = to.Ptr(score)
	return response
}

func recommendationResponseWithSplits(validUntil time.Time, id string, splits ...armrecommender.SKUMixPlacementItem) *capacityrecommendation.SKUMixPlacementScoresClientPostResponse {
	items := make([]*armrecommender.SKUMixPlacementItem, 0, len(splits))
	for i := range splits {
		items = append(items, &splits[i])
	}
	return withRegularPriority(&capacityrecommendation.SKUMixPlacementScoresClientPostResponse{
		SKUMixPlacementResponse: capacityrecommendation.SKUMixPlacementResponse{
			ValidUntil: to.Ptr(validUntil),
			PlacementChoices: []*armrecommender.SKUMixPlacementDeploymentChoice{
				{
					ID:       to.Ptr(id),
					Score:    to.Ptr(int32(9)),
					SKUSplit: items,
				},
			},
		},
	})
}
