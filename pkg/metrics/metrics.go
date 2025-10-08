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
	// Register all metrics here.
	// Pattern for adding new subsystem metrics:
	//   1. Create metric variables above (or in subsystem-specific files)
	//   2. Register them using crmetrics.Registry.MustRegister() or the helper below
	//   3. Use consistent naming: <subsystem>_<metric_name>_<unit>
	crmetrics.Registry.MustRegister(
		ImageSelectionErrorCount,
	)
}
