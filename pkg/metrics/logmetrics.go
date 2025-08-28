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

	"github.com/Azure/karpenter-provider-azure/pkg/metrics/logvalues"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	VMCreateStartMetric = &metric{
		name: "VMCreateStart",
	}
)

type metric struct {
	name string
}

func (m *metric) Emit(ctx context.Context, msg string, values ...logvalues.MetricValue) {
	logger := log.FromContext(ctx)

	// Each metric should emit its own name, under the "metric" key.
	fields := []any{
		"metric", m.name,
	}

	// Add all metric values using using their known key, and stored value
	for _, value := range values {
		key, val := value.ToKeyValuePair()
		fields = append(fields, key, val)
	}

	logger.Info(msg, fields...)
}
