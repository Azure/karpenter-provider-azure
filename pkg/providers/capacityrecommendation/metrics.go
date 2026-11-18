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
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armrecommender"
	metrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	capacityRecommendationSubsystem = "capacity_recommendation"
	metricValueUnknown              = "unknown"
	metricResultSuccess             = "success"
	metricResultError               = "error"
	metricResultHit                 = "hit"
	metricResultMiss                = "miss"
	metricPlacementScopeZonal       = "zonal"
	metricPlacementScopeRegional    = "regional"
)

var (
	// SKUMixPlacementRequestMetric tracks completed SKU Mix Placement API requests.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	SKUMixPlacementRequestMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metrics.Namespace,
			Subsystem: capacityRecommendationSubsystem,
			Name:      "requests_total",
			Help:      "Total number of SKU Mix Placement API requests completed.",
		},
		[]string{metrics.CapacityTypeLabel, metrics.AllocationStrategyLabel, metrics.OSTypeLabel, metrics.PlacementScopeLabel, metrics.ResultLabel},
	)

	// SKUMixPlacementCacheRequestMetric tracks SKU Mix Placement recommendation cache requests.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	SKUMixPlacementCacheRequestMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metrics.Namespace,
			Subsystem: capacityRecommendationSubsystem,
			Name:      "cache_requests_total",
			Help:      "Total number of SKU Mix Placement recommendation cache requests.",
		},
		[]string{metrics.CapacityTypeLabel, metrics.AllocationStrategyLabel, metrics.OSTypeLabel, metrics.PlacementScopeLabel, metrics.ResultLabel},
	)

	// SKUMixPlacementRequestDurationMetric tracks SKU Mix Placement API request latency.
	//
	// STABILITY: ALPHA - This metric may change or be removed without notice.
	SKUMixPlacementRequestDurationMetric = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metrics.Namespace,
			Subsystem: capacityRecommendationSubsystem,
			Name:      "request_duration_seconds",
			Help:      "Duration in seconds of SKU Mix Placement API requests.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 20},
		},
		[]string{metrics.CapacityTypeLabel, metrics.AllocationStrategyLabel, metrics.OSTypeLabel, metrics.PlacementScopeLabel, metrics.ResultLabel},
	)
)

type instrumentedSKUMixPlacementScoresAPI struct {
	client SKUMixPlacementScoresAPI
}

var _ SKUMixPlacementScoresAPI = &instrumentedSKUMixPlacementScoresAPI{}

func newInstrumentedSKUMixPlacementScoresAPI(client SKUMixPlacementScoresAPI) SKUMixPlacementScoresAPI {
	return &instrumentedSKUMixPlacementScoresAPI{client: client}
}

func recordCacheRequest(input *RankingInput, result string) {
	labels := inputMetricLabels(input)
	labels[metrics.ResultLabel] = result
	SKUMixPlacementCacheRequestMetric.With(labels).Inc()
}

func (c *instrumentedSKUMixPlacementScoresAPI) Post(
	ctx context.Context,
	location string,
	request armrecommender.SKUMixPlacementRequest,
	options *armrecommender.SKUMixPlacementScoresClientPostOptions,
) (armrecommender.SKUMixPlacementScoresClientPostResponse, error) {
	labels := requestMetricLabels(request)
	startedAt := time.Now()
	response, err := c.client.Post(ctx, location, request, options)
	labels[metrics.ResultLabel] = metricResultSuccess
	if err != nil {
		labels[metrics.ResultLabel] = metricResultError
	}
	SKUMixPlacementRequestMetric.With(labels).Inc()
	SKUMixPlacementRequestDurationMetric.With(labels).Observe(time.Since(startedAt).Seconds())
	return response, err
}

func inputMetricLabels(input *RankingInput) prometheus.Labels {
	placementScope := metricPlacementScopeRegional
	if len(input.Zones) > 0 {
		placementScope = metricPlacementScopeZonal
	}
	return prometheus.Labels{
		metrics.CapacityTypeLabel:       input.CapacityType,
		metrics.AllocationStrategyLabel: "prioritized", // all we support right now
		metrics.OSTypeLabel:             string(input.OSType),
		metrics.PlacementScopeLabel:     placementScope,
	}
}

func requestMetricLabels(request armrecommender.SKUMixPlacementRequest) prometheus.Labels {
	capacityType := metricValueUnknown
	allocationStrategy := metricValueUnknown
	osType := metricValueUnknown
	if request.CapacityProfile != nil {
		capacityType = capacityTypeMetricValue(request.CapacityProfile.Priority)
		allocationStrategy = allocationStrategyMetricValue(request.CapacityProfile.AllocationStrategy)
		osType = osTypeMetricValue(request.CapacityProfile.OSType)
	}

	placementScope := metricPlacementScopeRegional
	if len(request.Zones) > 0 {
		placementScope = metricPlacementScopeZonal
	}
	return prometheus.Labels{
		metrics.CapacityTypeLabel:       capacityType,
		metrics.AllocationStrategyLabel: allocationStrategy,
		metrics.OSTypeLabel:             osType,
		metrics.PlacementScopeLabel:     placementScope,
	}
}

func capacityTypeMetricValue(priority *armrecommender.SKUMixPlacementPriority) string {
	switch lo.FromPtr(priority) {
	case armrecommender.SKUMixPlacementPriorityRegular:
		return karpv1.CapacityTypeOnDemand
	case armrecommender.SKUMixPlacementPrioritySpot:
		return karpv1.CapacityTypeSpot
	default:
		return metricValueUnknown
	}
}

func allocationStrategyMetricValue(allocationStrategy *armrecommender.SKUMixPlacementAllocationStrategy) string {
	return strings.ToLower(string(lo.FromPtrOr(allocationStrategy, armrecommender.SKUMixPlacementAllocationStrategy(metricValueUnknown))))
}

func osTypeMetricValue(osType *armrecommender.SKUMixPlacementOSType) string {
	return strings.ToLower(string(lo.FromPtrOr(osType, armrecommender.SKUMixPlacementOSType(metricValueUnknown))))
}

func init() {
	crmetrics.Registry.MustRegister(
		SKUMixPlacementRequestMetric,
		SKUMixPlacementCacheRequestMetric,
		SKUMixPlacementRequestDurationMetric,
	)
}
