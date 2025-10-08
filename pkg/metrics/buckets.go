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

// Standard histogram buckets for different operation types.
// These buckets are tuned for typical Azure operation latencies.
var (
	// APICallBuckets for fast API operations (100ms to 5 minutes).
	// Use for: Azure ARM API calls, quick metadata lookups.
	APICallBuckets = []float64{.1, .25, .5, 1, 2.5, 5, 10, 15, 25, 50, 120, 300}

	// VMOperationBuckets for slower VM operations (1 second to 10 minutes).
	// Use for: VM creation, deletion, scaling operations.
	VMOperationBuckets = []float64{1, 5, 10, 15, 30, 60, 120, 180, 300, 600}

	// QuickOperationBuckets for sub-second operations (10ms to 5 seconds).
	// Use for: Cache lookups, local computations, fast validations.
	QuickOperationBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5}

	// DefaultBuckets is a sensible default for general operations.
	// Falls back to Prometheus default buckets.
	DefaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
)
