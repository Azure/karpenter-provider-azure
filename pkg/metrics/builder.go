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
	"github.com/prometheus/client_golang/prometheus"
)

// SubsystemMetrics holds common metrics for a subsystem.
// This provides a standard set of metrics that can be used across different
// subsystems to maintain consistency in metric naming and labeling.
type SubsystemMetrics struct {
	DurationHistogram *prometheus.HistogramVec
	ErrorCounter      *prometheus.CounterVec
	SuccessCounter    *prometheus.CounterVec
}

// NewSubsystemMetrics creates a standard set of metrics for a subsystem.
// This builder pattern makes it easy to add consistent metrics for new subsystems.
//
// Parameters:
//   - subsystem: The subsystem name (e.g., "instance", "pricing")
//   - labels: Common labels to apply to all metrics in this subsystem
//   - buckets: Histogram buckets for duration measurements (use predefined buckets from buckets.go)
//
// Returns a SubsystemMetrics struct with initialized metrics that can be registered.
func NewSubsystemMetrics(subsystem string, labels []string, buckets []float64) *SubsystemMetrics {
	return &SubsystemMetrics{
		DurationHistogram: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: subsystem,
				Name:      "duration_seconds",
				Help:      "Time taken to complete operations in seconds.",
				Buckets:   buckets,
			},
			labels,
		),
		ErrorCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: subsystem,
				Name:      "errors_total",
				Help:      "Total number of errors encountered.",
			},
			labels,
		),
		SuccessCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: subsystem,
				Name:      "success_total",
				Help:      "Total number of successful operations.",
			},
			labels,
		),
	}
}

// Collectors returns all collectors for registration with Prometheus.
// This helper makes it easy to register all metrics from a subsystem at once.
func (sm *SubsystemMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		sm.DurationHistogram,
		sm.ErrorCounter,
		sm.SuccessCounter,
	}
}
