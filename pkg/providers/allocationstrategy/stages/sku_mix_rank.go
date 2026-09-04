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
	"context"
	"fmt"
	"sort"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/capacityrecommendation"
	azurezones "github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const (
	maxRecommendationVMSizes = 10
	initialRequestCount      = int32(10) // TODO: We could make this dynamic later, possibly based on an actual rolling request history window
)

type skuMixRankStage struct {
	provider capacityrecommendation.Provider
	mode     string
}

type recommendationGroupKey struct {
	capacityType   string
	placementScope string
}

type recommendationGroup struct {
	key       recommendationGroupKey
	vmSizes   []string
	vmSizeSet sets.Set[string]
	zones     []string
	zoneSet   sets.Set[string]
	vmZones   map[string]sets.Set[string]
	osType    corev1.OSName
}

func NewSKUMixRankStage(provider capacityrecommendation.Provider, mode string) Stage {
	return &skuMixRankStage{
		provider: provider,
		mode:     mode,
	}
}

func (s *skuMixRankStage) Process(ctx context.Context, instanceOfferings []InstanceOffering) []InstanceOffering {
	groups := buildRecommendationGroups(instanceOfferings)
	recommendedOfferings := sets.New[string]()

	for _, group := range groups {
		if len(group.vmSizes) == 0 {
			continue
		}
		recommendations, err := s.provider.GetRecommendations(
			ctx,
			&capacityrecommendation.RankingInput{
				VMSizes:      group.vmSizes,
				Zones:        group.zones,
				CapacityType: group.key.capacityType,
				OSType:       group.osType,
				Count:        initialRequestCount,
			},
		)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to get SKU Mix Placement recommendation",
				"capacityType", group.key.capacityType,
				"placementScope", group.key.placementScope,
				"zones", group.zones,
			)
			if s.mode == consts.ComputeRecommendationModeEnabled {
				// If our API failed, we want to fail open so add all items in the group to recommended
				// Note that the group only has maxRecommendationVMSizes so even in the case the API fails
				// we won't be recommending more than that number of VM sizes to the caller.
				addLocalCandidates(group, recommendedOfferings)
			}
			continue
		}
		if len(recommendations) == 0 {
			if s.mode == consts.ComputeRecommendationModeEnabled {
				// If our API failed, we want to fail open so add all items in the group to recommended
				// Note that the group only has maxRecommendationVMSizes so even in the case the API fails
				// we won't be recommending more than that number of VM sizes to the caller.
				addLocalCandidates(group, recommendedOfferings)
			}
			continue
		}

		comparison := compareRecommendationGroup(group, recommendations)
		log.FromContext(ctx).Info("compared SKU Mix Placement recommendations with local ranking",
			"capacityType", group.key.capacityType,
			"placementScope", group.key.placementScope,
			"splitID", recommendations[0].ID,
			"mode", s.mode,
			"localRanking", comparison.localRanking,
			"recommendedRanking", comparison.recommendedRanking,
			"hasDifferences", len(comparison.differences) > 0,
			"differences", comparison.differences,
		)

		if s.mode != consts.ComputeRecommendationModeEnabled {
			continue
		}
		for _, recommendation := range recommendations {
			key := offeringKey(recommendation.VMSize, recommendation.CapacityType, group.key.placementScope, recommendation.Zone)
			recommendedOfferings.Insert(key)
		}
	}

	// The net result here is that we're just using the SKU Mix API to filter offerings. It does NOT reorder/reprioritize them
	// currently due to the need to join the (up to) 4 recommendation groups into a single list. We have an outstanding ask to
	// the team to allow us to ask for spot + dedicated (at least) in a single list so that we can get back a true "price-prioritized"
	// list of recommendations without having to join the two lists together (which requires some external source of truth to order between
	// the two lists). Once we have that, we can remove the re-ranking step below and rely on the SKU API to provide the correct ordering.
	if s.mode == consts.ComputeRecommendationModeEnabled {
		instanceOfferings = filterOfferingsByRecommended(instanceOfferings, recommendedOfferings)
		// TODO: Currently we re-rank instance offerings because we don't 100% trust the SKU API response (see comments on placementChoiceIsBetter in capacityrecommendation.go).
		// Once we have more confidence in the API, we can remove this re-ranking step and rely on the SKU API to provide the correct ordering.
		// We will still need to join the (up to) 4 recommendation groups into a single list somehow though...
		instanceOfferings = rankInstanceOfferings(instanceOfferings)
	}
	return instanceOfferings
}

func buildRecommendationGroups(instanceOfferings []InstanceOffering) []*recommendationGroup {
	groupsByKey := map[recommendationGroupKey]*recommendationGroup{}
	// Offerings can produce at most four groups: spot/on-demand crossed with zonal/regional placement scope.
	groups := make([]*recommendationGroup, 0, 4)

	for _, instanceOffering := range instanceOfferings {
		for _, offering := range instanceOffering.Offerings {
			key := recommendationGroupKey{
				capacityType:   offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any(),
				placementScope: azurezones.PlacementScopeForOffering(offering),
			}
			group, ok := groupsByKey[key]
			if !ok {
				group = &recommendationGroup{
					key:       key,
					vmSizeSet: sets.New[string](),
					zoneSet:   sets.New[string](),
					vmZones:   map[string]sets.Set[string]{},
					osType:    instanceTypeOS(instanceOffering.InstanceType),
				}
				groupsByKey[key] = group
				groups = append(groups, group)
			}

			if !group.vmSizeSet.Has(instanceOffering.InstanceType.Name) && len(group.vmSizes) < maxRecommendationVMSizes {
				group.vmSizeSet.Insert(instanceOffering.InstanceType.Name)
				group.vmSizes = append(group.vmSizes, instanceOffering.InstanceType.Name)
			} else if !group.vmSizeSet.Has(instanceOffering.InstanceType.Name) {
				continue // skip because max recommendation count reached
			}

			if _, ok := group.vmZones[instanceOffering.InstanceType.Name]; !ok {
				group.vmZones[instanceOffering.InstanceType.Name] = sets.New[string]()
			}
			// TODO: Since we're collapsing zones across multiple sizes, if some sizes aren't available in some zones,
			// we'll present those zones to the recommendation API as available for all sizes. This is probably fine
			// as presumably it knows those sizes won't work for those zones and will not recommend them anyway.
			if key.placementScope == v1beta1.PlacementScopeZonal {
				zone := armZone(offering)
				if zone != "" {
					group.vmZones[instanceOffering.InstanceType.Name].Insert(zone)
					if !group.zoneSet.Has(zone) {
						group.zoneSet.Insert(zone)
						group.zones = append(group.zones, zone)
					}
				}
			}
		}
	}
	return groups
}

// groupedOffering is used only for logging
type groupedOffering struct {
	VMSize string   `json:"vmSize"`
	Zones  []string `json:"zones"`
}

// offeringDifference is used only for logging
type offeringDifference struct {
	VMSize          string   `json:"vmSize"`
	LocalRank       int      `json:"localRank,omitempty"`
	RecommendedRank int      `json:"recommendedRank,omitempty"`
	MissingZones    []string `json:"missingZones,omitempty"`
	AdditionalZones []string `json:"additionalZones,omitempty"`
}

// recommendationComparison is used only for logging
type recommendationComparison struct {
	localRanking       []groupedOffering
	recommendedRanking []groupedOffering
	differences        []offeringDifference
}

// normalizedRanking is used only for logging
type normalizedRanking struct {
	order       []string
	zonesBySize map[string]sets.Set[string]
}

// compareRecommendationGroup compares the local ranking of VM sizes and zones with the recommended ranking from the SKU Mix Placement API.
// This is only used for logging.
func compareRecommendationGroup(group *recommendationGroup, recommendations []capacityrecommendation.Recommendation) recommendationComparison {
	local := normalizeLocalRanking(group)
	recommended := normalizeRecommendedRanking(group.key.capacityType, recommendations)
	return recommendationComparison{
		localRanking:       local.groupedOfferings(),
		recommendedRanking: recommended.groupedOfferings(),
		differences:        compareRankings(local, recommended),
	}
}

func normalizeLocalRanking(group *recommendationGroup) normalizedRanking {
	return normalizedRanking{
		order:       append([]string(nil), group.vmSizes...),
		zonesBySize: group.vmZones,
	}
}

func normalizeRecommendedRanking(capacityType string, recommendations []capacityrecommendation.Recommendation) normalizedRanking {
	ranking := normalizedRanking{zonesBySize: map[string]sets.Set[string]{}}
	for _, recommendation := range recommendations {
		if recommendation.CapacityType != capacityType {
			continue
		}
		if _, ok := ranking.zonesBySize[recommendation.VMSize]; !ok {
			ranking.zonesBySize[recommendation.VMSize] = sets.New[string]()
			ranking.order = append(ranking.order, recommendation.VMSize)
		}
		if recommendation.Zone != "" {
			ranking.zonesBySize[recommendation.VMSize].Insert(recommendation.Zone)
		}
	}
	return ranking
}

func (r normalizedRanking) groupedOfferings() []groupedOffering {
	result := make([]groupedOffering, 0, len(r.order))
	for _, vmSize := range r.order {
		result = append(result, groupedOffering{VMSize: vmSize, Zones: sortedZones(r.zones(vmSize))})
	}
	return result
}

func (r normalizedRanking) zones(vmSize string) sets.Set[string] {
	if zones := r.zonesBySize[vmSize]; zones != nil {
		return zones
	}
	return sets.New[string]()
}

func compareRankings(local, recommended normalizedRanking) []offeringDifference {
	localRanks := ranksBySize(local.order)
	recommendedRanks := ranksBySize(recommended.order)
	differences := make([]offeringDifference, 0)
	for _, vmSize := range lo.Uniq(lo.Concat(local.order, recommended.order)) {
		localZones := local.zones(vmSize)
		recommendedZones := recommended.zones(vmSize)
		difference := offeringDifference{
			VMSize:          vmSize,
			LocalRank:       localRanks[vmSize],
			RecommendedRank: recommendedRanks[vmSize],
			MissingZones:    sortedZones(localZones.Difference(recommendedZones)),
			AdditionalZones: sortedZones(recommendedZones.Difference(localZones)),
		}
		if difference.LocalRank != difference.RecommendedRank || len(difference.MissingZones) > 0 || len(difference.AdditionalZones) > 0 {
			differences = append(differences, difference)
		}
	}
	return differences
}

func ranksBySize(order []string) map[string]int {
	ranks := make(map[string]int, len(order))
	for rank, vmSize := range order {
		ranks[vmSize] = rank + 1
	}
	return ranks
}

func sortedZones(zones sets.Set[string]) []string {
	result := zones.UnsortedList()
	sort.Strings(result)
	return result
}

func addLocalCandidates(group *recommendationGroup, candidates sets.Set[string]) {
	zones := group.zones
	if group.key.placementScope == v1beta1.PlacementScopeRegional {
		zones = []string{""}
	}
	for _, vmSize := range group.vmSizes {
		for _, zone := range zones {
			key := offeringKey(vmSize, group.key.capacityType, group.key.placementScope, zone)
			candidates.Insert(key)
		}
	}
}

func instanceTypeOS(instanceType *corecloudprovider.InstanceType) corev1.OSName {
	if instanceType != nil {
		switch corev1.OSName(instanceType.Requirements.Get(corev1.LabelOSStable).Any()) {
		case corev1.Windows:
			return corev1.Windows
		case corev1.Linux:
			return corev1.Linux
		}
	}
	return corev1.Linux
}

func armZone(offering *corecloudprovider.Offering) string {
	zone := offering.Requirements.Get(corev1.LabelTopologyZone).Any()
	armZones := azurezones.MakeARMZonesFromAKSLabelZone(zone)
	if len(armZones) == 0 || armZones[0] == nil {
		return ""
	}
	return *armZones[0]
}

func filterOfferingsByRecommended(instanceOfferings []InstanceOffering, recommended sets.Set[string]) []InstanceOffering {
	filtered := instanceOfferings[:0] // in-place filtering to avoid allocating array copy
	for i := range instanceOfferings {
		name := instanceOfferings[i].InstanceType.Name
		offerings := instanceOfferings[i].Offerings[:0] // in-place filtering to avoid allocating array copy
		for _, offering := range instanceOfferings[i].Offerings {
			if recommended.Has(offeringKeyForOffering(name, offering)) {
				offerings = append(offerings, offering)
			}
		}
		instanceOfferings[i].Offerings = offerings
		if len(offerings) == 0 {
			continue
		}
		filtered = append(filtered, instanceOfferings[i])
	}
	return filtered
}

func offeringKey(name string, capacityType string, placementScope string, zone string) string {
	return fmt.Sprintf("%s|%s|%s|%s", name, capacityType, placementScope, zone)
}

func offeringKeyForOffering(name string, offering *corecloudprovider.Offering) string {
	capacityType := offering.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
	placementScope := azurezones.PlacementScopeForOffering(offering)
	zone := ""
	if placementScope == v1beta1.PlacementScopeZonal {
		zone = armZone(offering)
	}
	return offeringKey(name, capacityType, placementScope, zone)
}
