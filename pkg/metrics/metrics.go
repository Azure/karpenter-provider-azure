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

package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

// Note: If this grows too large, this package could be splitted into multiple, one per subsystem.
// That pattern is being used in Karpenter core (as of v1.5.0).
var (
	ImageSelectionErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: imageFamilySubsystem,
			Name:      "selection_error_count",
			Help:      "The number of errors encountered while selecting an image.",
		},
		[]string{"family"},
	)

	MethodDurationWithAsync = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: cloudProviderSubsystem,
			Name:      "duration_seconds_with_async",
			Help:      "Duration of cloud provider method calls. Includes async/LRO operations in routines that run beyond the initial call.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{metricLabelController, metricLabelMethod, metricLabelError},
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		ImageSelectionErrorCount,
		MethodDurationWithAsync,
	)
}

func GetLabelsMapForCloudProviderDurationWithAsync(ctx context.Context, method string, err error) map[string]string {
	// TODO
	return map[string]string{
		metricLabelController: injection.GetControllerName(ctx),
		metricLabelMethod:     method,
		metricLabelError: func() string {
			switch {
			case err == nil:
				return cloudProviderMetricLabelErrorNone
			default:
				return cloudProviderMetricLabelErrorUnknown
			}
		}(),
	}
}
