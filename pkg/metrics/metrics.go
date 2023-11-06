// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

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
)

func init() {
	crmetrics.Registry.MustRegister(
		ImageSelectionErrorCount,
	)
}
