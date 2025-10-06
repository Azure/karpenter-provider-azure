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

	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	VMCreateStartMetric = &metric{
		name: "VMCreateStart",
	}
	VMCreateSyncFailureMetric = &metric{
		name: "VMCreateSyncFailure",
	}
	VMCreateAsyncFailureMetric = &metric{
		name: "VMCreateAsyncFailure",
	}
	VMCreateResponseError = &metric{
		name: "ResponseError",
	}
)

type metric struct {
	name string
}

// Emulating the prometheus method here:
// https://github.com/awslabs/operatorpkg/blob/e9977193119b38a3f85ebb7df4f0543a8b5a2a20/metrics/prometheus.go#L17
// > Note: since we are logging behind the scenes, rather that emitting an actual prometheus metric we do still accept
// > a context, and msg for the logging.
func (m *metric) Inc(ctx context.Context, msg string, values ...Value) {
	logger := log.FromContext(ctx)

	// Each metric should emit its own name, under the "metric" key.
	fields := []any{
		"metric", m.name,
	}

	// Get and include the set of metrics key value pairs.
	fields = append(fields, ValuesToKeyValuePairs(values...)...)

	logger.Info(msg, fields...)
}
