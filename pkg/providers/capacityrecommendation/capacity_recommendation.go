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

package capacityrecommendation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	// defaultCacheTTL is the default cache duration if the recommendation response does not include a validUntil value.
	// Set to 45s since we expect the server to give a value of about ~60s.
	defaultCacheTTL = 45 * time.Second
	// TODO: Need better metrics and to break calls out of hot path (+ parallelize?) in the future.
	requestTimeout = 15 * time.Second
)

type SKUMixPlacementScoresAPI interface {
	Post(
		ctx context.Context,
		location string,
		skuMixPlacementRequest armrecommender.SKUMixPlacementRequest,
		options *armrecommender.SKUMixPlacementScoresClientPostOptions,
	) (armrecommender.SKUMixPlacementScoresClientPostResponse, error)
}

// Provider supplies capacity-aware VM placement recommendations.
type Provider interface {
	GetRecommendations(ctx context.Context, input *RankingInput) (RecommendationDetails, error)
}

// RankingInput identifies an allocation request to rank.
type RankingInput struct {
	VMSizes      []string
	Zones        []string
	CapacityType string
	OSType       corev1.OSName
	Count        int32
}

// Recommendation is one VM allocation from an ordered placement choice.
type Recommendation struct {
	VMSize       string
	Zone         string
	CapacityType string
	// Score of the recommendation. Higher is better.
	Score int32
	// ID is the ID of the recommendation. This can/should be passed along to CRP.
	ID string
	// Count is not currently used
	Count int32
}

// RecommendationDetails contains placement recommendations and the capacity limits used to produce them.
type RecommendationDetails struct {
	Recommendations []Recommendation
	CapacityLimits  []*armrecommender.SKUMixPlacementCapacityLimit
}

// DefaultProvider obtains and reactively caches SKU Mix Placement recommendations.
type DefaultProvider struct {
	client     SKUMixPlacementScoresAPI
	cache      *cache.Cache
	location   string
	defaultTTL time.Duration
	sfGroup    singleflight.Group
}

var _ Provider = &DefaultProvider{}

// NewProvider creates a capacity recommendation provider using the supplied
// per-response expiry cache.
func NewProvider(client SKUMixPlacementScoresAPI, cache *cache.Cache, location string) *DefaultProvider {
	return &DefaultProvider{
		client:     client,
		cache:      cache,
		location:   location,
		defaultTTL: defaultCacheTTL,
	}
}

// GetRecommendations returns cached or freshly generated recommendations.
func (p *DefaultProvider) GetRecommendations(ctx context.Context, input *RankingInput) (RecommendationDetails, error) {
	if err := validateInput(input); err != nil {
		return RecommendationDetails{}, fmt.Errorf("invalid SKU Mix Placement recommendation input: %w", err)
	}

	key, err := cacheKey(input)
	if err != nil {
		return RecommendationDetails{}, fmt.Errorf("hashing SKU Mix Placement recommendation input: %w", err)
	}
	if result, ok := p.getCached(key); ok {
		return result, nil
	}
	value, err, _ := p.sfGroup.Do(key, func() (any, error) {
		// check the cache again in case a different caller in sfGroup already fetched and cached the result
		if result, ok := p.getCached(key); ok {
			return result, nil
		}
		return p.fetchAndCache(ctx, key, input)
	})
	if err != nil {
		return RecommendationDetails{}, err
	}

	result, ok := value.(RecommendationDetails)
	if !ok {
		return RecommendationDetails{}, fmt.Errorf("unexpected recommendation result type %T", value)
	}
	return cloneRecommendationDetails(result), nil
}

func (p *DefaultProvider) fetchAndCache(ctx context.Context, key string, input *RankingInput) (RecommendationDetails, error) {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	logger := log.FromContext(ctx)

	response, err := p.client.Post(requestCtx, p.location, toSKUMixPlacementRequest(input), nil)
	if err != nil {
		return RecommendationDetails{}, err
	}

	if logger.V(2).Enabled() {
		logger.V(2).Info(
			"received SKU Mix Placement API response",
			"response", skuMixPlacementResponseJSON(response),
		)
	}

	choices := sortedPlacementChoices(response, input)
	if len(choices) == 0 {
		return RecommendationDetails{}, fmt.Errorf("SKU Mix Placement response contained no placement choices")
	}
	if response.PlacementChoices[0] != choices[0] {
		logger.Error(
			fmt.Errorf("expected top SKU Mix Placement choice differs from first response choice"),
			"sorted non-first SKU Mix Placement choice first",
			"ourChoice", placementChoiceJSON(choices[0]),
			"placementChoice", placementChoiceJSON(response.PlacementChoices[0]),
		)
	}
	recommendations, err := recommendationsFromChoices(lo.FromPtr(response.ID), choices)
	if err != nil {
		return RecommendationDetails{}, err
	}
	result := RecommendationDetails{
		Recommendations: recommendations,
		CapacityLimits:  response.CapacityLimits,
	}

	validUntil := time.Now().Add(p.defaultTTL)
	if response.ValidUntil != nil {
		validUntil = *response.ValidUntil
	}
	ttl := time.Until(validUntil)
	// Hedge against possible short cache TTLs from the API by caching for at least the default TTL.
	if ttl < p.defaultTTL {
		logger.V(1).Info(
			"SKU Mix Placement recommendation response contained a short cache TTL; using default TTL instead",
			"validUntil", validUntil,
			"ttl", ttl,
			"defaultTTL", p.defaultTTL,
		)
		ttl = p.defaultTTL
	}

	p.cache.Set(key, result, ttl)
	return cloneRecommendationDetails(result), nil
}

func (p *DefaultProvider) getCached(key string) (RecommendationDetails, bool) {
	value, ok := p.cache.Get(key)
	if !ok {
		return RecommendationDetails{}, false
	}
	result, ok := value.(RecommendationDetails)
	if !ok {
		return RecommendationDetails{}, false
	}
	return cloneRecommendationDetails(result), true
}

func toSKUMixPlacementRequest(input *RankingInput) armrecommender.SKUMixPlacementRequest {
	priority := armrecommender.SKUMixPlacementPriorityRegular
	if input.CapacityType == karpv1.CapacityTypeSpot {
		priority = armrecommender.SKUMixPlacementPrioritySpot
	}
	// Note: osType is a required field of input so it will always be specified
	var osType armrecommender.SKUMixPlacementOSType
	switch input.OSType {
	case corev1.Linux:
		osType = armrecommender.SKUMixPlacementOSTypeLinux
	case corev1.Windows:
		osType = armrecommender.SKUMixPlacementOSTypeWindows
	}
	vmSizes := make([]*armrecommender.SKUMixPlacementVMSize, 0, len(input.VMSizes))
	for rank, name := range input.VMSizes {
		vmSizes = append(vmSizes, &armrecommender.SKUMixPlacementVMSize{
			Name: to.Ptr(name),
			Rank: to.Ptr(int32(rank)),
		})
	}
	zones := make([]*string, 0, len(input.Zones))
	for _, zone := range input.Zones {
		zones = append(zones, to.Ptr(zone))
	}
	return armrecommender.SKUMixPlacementRequest{
		Zones: zones,
		CapacityProfile: &armrecommender.SKUMixPlacementCapacityProfile{
			Capacity:           to.Ptr(input.Count),
			CapacityType:       to.Ptr(armrecommender.SKUMixPlacementCapacityTypeVM),
			Priority:           to.Ptr(priority),
			AllocationStrategy: to.Ptr(armrecommender.SKUMixPlacementAllocationStrategyPrioritized),
			OSType:             to.Ptr(osType),
		},
		InstanceDescription: &armrecommender.SKUMixPlacementInstanceDescription{
			VMSizes: vmSizes,
		},
	}
}

// sortedPlacementChoices ensures that choices follow the order we believe is best in the presence of score ties.
// Today the API can return choices in an order that differs from this comparison, so sort a copy while preserving
// API order for complete ties.
func sortedPlacementChoices(response armrecommender.SKUMixPlacementScoresClientPostResponse, input *RankingInput) []*armrecommender.SKUMixPlacementDeploymentChoice {
	choices := make([]*armrecommender.SKUMixPlacementDeploymentChoice, 0, len(response.PlacementChoices))
	for _, choice := range response.PlacementChoices {
		if choice == nil {
			continue
		}
		choices = append(choices, choice)
	}
	sort.SliceStable(choices, func(i, j int) bool {
		return placementChoiceIsBetter(choices[i], choices[j], input)
	})
	return choices
}

type recommendationKey struct {
	vmSize       string
	zone         string
	capacityType string
}

func recommendationsFromChoices(responseID string, choices []*armrecommender.SKUMixPlacementDeploymentChoice) ([]Recommendation, error) {
	seen := sets.New[recommendationKey]()
	result := make([]Recommendation, 0)
	for _, choice := range choices {
		if choice.Score == nil || choice.SKUSplit == nil {
			return nil, fmt.Errorf("SKU Mix Placement response for ID: %s contained an invalid placement choice", responseID)
		}
		for _, split := range choice.SKUSplit {
			recommendation, err := recommendationFromSplit(responseID, choice, split)
			if err != nil {
				return nil, err
			}
			key := recommendation.key()
			// There may be multiple splits with the same VM size, zone, and capacity type in a single API response.
			// We prefer the "best" split (highest score).
			if seen.Has(key) {
				continue
			}
			seen.Insert(key)
			result = append(result, recommendation)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("SKU Mix Placement response contained no recommended VM sizes")
	}
	return result, nil
}

func recommendationFromSplit(responseID string, choice *armrecommender.SKUMixPlacementDeploymentChoice, split *armrecommender.SKUMixPlacementItem) (Recommendation, error) {
	if split == nil || split.Name == nil || split.Capacity == nil || split.Priority == nil {
		return Recommendation{}, fmt.Errorf("SKU Mix Placement response for ID: %s contained an invalid SKU split", responseID)
	}
	capacityType, ok := capacityTypeForPriority(*split.Priority)
	if !ok {
		return Recommendation{}, fmt.Errorf("SKU Mix Placement response contained unsupported priority %q", *split.Priority)
	}
	return Recommendation{
		VMSize:       *split.Name,
		Zone:         lo.FromPtr(split.Zone),
		CapacityType: capacityType,
		Score:        lo.FromPtr(choice.Score),
		ID:           responseID,
		Count:        lo.FromPtr(split.Capacity),
	}, nil
}

func (r Recommendation) key() recommendationKey {
	return recommendationKey{
		vmSize:       r.VMSize,
		zone:         r.Zone,
		capacityType: r.CapacityType,
	}
}

func capacityTypeForPriority(priority armrecommender.SKUMixPlacementPriority) (string, bool) {
	switch priority {
	case armrecommender.SKUMixPlacementPriorityRegular:
		return karpv1.CapacityTypeOnDemand, true
	case armrecommender.SKUMixPlacementPrioritySpot:
		return karpv1.CapacityTypeSpot, true
	default:
		return "", false
	}
}

func placementChoiceJSON(choice *armrecommender.SKUMixPlacementDeploymentChoice) string {
	value, err := json.Marshal(choice)
	if err != nil {
		return fmt.Sprintf("<failed to marshal placement choice: %s>", err)
	}
	return string(value)
}

func skuMixPlacementResponseJSON(response armrecommender.SKUMixPlacementScoresClientPostResponse) string {
	value, err := json.Marshal(response.SKUMixPlacementResponse)
	if err != nil {
		return fmt.Sprintf("<failed to marshal SKU Mix Placement response: %s>", err)
	}
	return string(value)
}

// TODO: This function exists because we have observed some "strange" placements from the SKU Mix Placement API.
// for example, when we ask for prioritized list of these 3 skus:
// Standard_D8s_v5
// Standard_D2s_v3
// Standard_E2s_v3
// Split API suggested that the split: Standard_D2s_v3 (all 3 zones) was higher ranked than Standard_D8s_v5 (z1, z2) + Standard_D2s_v3 (z3).
// This seems wrong to me as it's not following the prioritized list of skus we provided.
// So for now, we do our own placement ordering on-top and log when we see a difference.
func placementChoiceIsBetter(
	candidate *armrecommender.SKUMixPlacementDeploymentChoice,
	current *armrecommender.SKUMixPlacementDeploymentChoice,
	input *RankingInput,
) bool {
	currentScore := lo.FromPtr(current.Score)
	candidateScore := lo.FromPtr(candidate.Score)
	// Only tiebreak among sizes that are equally scored in the API response
	if candidateScore != currentScore {
		return candidateScore > currentScore
	}

	requestedZones := sets.New(input.Zones...)

	// Compare requested SKUs in input priority order. For
	// each SKU, prefer more allocated capacity, then distribution across more
	// requested zones. Only consider the next-ranked SKU when both are tied.
	for _, vmSize := range input.VMSizes {
		candidateCapacity, candidateZones := skuStats(candidate, vmSize, requestedZones)
		currentCapacity, currentZones := skuStats(current, vmSize, requestedZones)
		if candidateCapacity != currentCapacity {
			return candidateCapacity > currentCapacity
		}
		if candidateZones != currentZones {
			return candidateZones > currentZones
		}
	}

	// If the choices allocate the requested SKUs equally, prefer the one spanning more
	// of the requested zones. Preserve API order when both are equal.
	return requestedZoneCoverage(candidate, requestedZones) > requestedZoneCoverage(current, requestedZones)
}

func skuStats(choice *armrecommender.SKUMixPlacementDeploymentChoice, vmSize string, requestedZones sets.Set[string]) (int32, int) {
	var capacity int32
	zones := sets.New[string]()
	for _, splitItem := range choice.SKUSplit {
		if splitItem == nil || lo.FromPtr(splitItem.Name) != vmSize {
			continue
		}
		// TODO: MaxCapacity when they have it?
		capacity += lo.FromPtr(splitItem.Capacity)
		if splitItem.Zone == nil {
			continue
		}
		zone := lo.FromPtr(splitItem.Zone)
		if requestedZones.Has(zone) {
			zones.Insert(zone)
		}
	}
	return capacity, zones.Len()
}

func requestedZoneCoverage(choice *armrecommender.SKUMixPlacementDeploymentChoice, requestedZones sets.Set[string]) int {
	covered := sets.New[string]()
	for _, split := range choice.SKUSplit {
		if split == nil || split.Zone == nil {
			continue
		}
		if requestedZones.Has(*split.Zone) {
			covered.Insert(*split.Zone)
		}
	}
	return covered.Len()
}

func validateInput(input *RankingInput) error {
	if input == nil {
		return fmt.Errorf("input is nil")
	}
	if len(input.VMSizes) == 0 {
		return fmt.Errorf("no VM sizes specified")
	}
	if input.Count <= 0 {
		return fmt.Errorf("count %d is outside the supported range", input.Count)
	}
	if input.CapacityType != karpv1.CapacityTypeOnDemand && input.CapacityType != karpv1.CapacityTypeSpot {
		return fmt.Errorf("unsupported capacity type %q", input.CapacityType)
	}
	if input.OSType != corev1.Linux && input.OSType != corev1.Windows {
		return fmt.Errorf("unsupported OS type %q", input.OSType)
	}
	return nil
}

func cacheKey(input *RankingInput) (string, error) {
	zones := append([]string(nil), input.Zones...)
	sort.Strings(zones)
	value, err := hashstructure.Hash(struct {
		CapacityType string
		OSType       corev1.OSName
		VMSizes      []string
		Zones        []string
	}{
		CapacityType: input.CapacityType,
		OSType:       input.OSType,
		VMSizes:      input.VMSizes,
		Zones:        zones,
	}, hashstructure.FormatV2, nil)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", value), nil
}

func cloneRecommendationDetails(in RecommendationDetails) RecommendationDetails {
	result := RecommendationDetails{
		Recommendations: append([]Recommendation(nil), in.Recommendations...),
		CapacityLimits:  make([]*armrecommender.SKUMixPlacementCapacityLimit, len(in.CapacityLimits)),
	}
	for i, capacityLimit := range in.CapacityLimits {
		if capacityLimit == nil {
			continue
		}
		cloned := *capacityLimit
		cloned.Limit = clonePointer(capacityLimit.Limit)
		cloned.Name = clonePointer(capacityLimit.Name)
		cloned.Priority = clonePointer(capacityLimit.Priority)
		cloned.Reason = clonePointer(capacityLimit.Reason)
		cloned.Zone = clonePointer(capacityLimit.Zone)
		result.CapacityLimits[i] = &cloned
	}
	return result
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
