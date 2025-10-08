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
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricContext provides a consistent way to measure operation duration and record results.
// This follows the pattern from cloud-provider-azure's MetricContext to ensure
// consistent metric recording across all subsystems.
//
// Example usage:
//
//	mc := NewMetricContext("instance", prometheus.Labels{"operation": "create"})
//	defer func() {
//	    if err != nil {
//	        mc.ObserveError(instanceMetrics.ErrorCounter)
//	    } else {
//	        mc.ObserveSuccess(instanceMetrics.SuccessCounter)
//	    }
//	    mc.ObserveDuration(instanceMetrics.DurationHistogram)
//	}()
type MetricContext struct {
	start     time.Time
	labels    prometheus.Labels
	subsystem string
}

// NewMetricContext creates a new metric context for tracking operation metrics.
// Call this at the start of an operation you want to measure.
//
// Parameters:
//   - subsystem: The subsystem name (should match the metrics being recorded)
//   - labels: Labels to apply to the metrics (e.g., operation name, resource group)
func NewMetricContext(subsystem string, labels prometheus.Labels) *MetricContext {
	return &MetricContext{
		start:     time.Now(),
		labels:    labels,
		subsystem: subsystem,
	}
}

// ObserveDuration records the duration of the operation to the provided histogram.
// Typically called in a defer function after the operation completes.
func (mc *MetricContext) ObserveDuration(histogram *prometheus.HistogramVec) {
	if histogram != nil {
		histogram.With(mc.labels).Observe(time.Since(mc.start).Seconds())
	}
}

// ObserveError increments the error counter with the context's labels.
// Call this when an operation fails.
func (mc *MetricContext) ObserveError(counter *prometheus.CounterVec) {
	if counter != nil {
		counter.With(mc.labels).Inc()
	}
}

// ObserveSuccess increments the success counter with the context's labels.
// Call this when an operation completes successfully.
func (mc *MetricContext) ObserveSuccess(counter *prometheus.CounterVec) {
	if counter != nil {
		counter.With(mc.labels).Inc()
	}
}
