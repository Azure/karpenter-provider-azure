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

package cloudprovider

import (
	"context"

	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

const (
	metricLabelController = "controller"
	metricLabelMethod     = "method"
	metricLabelProvider   = "provider"
	metricLabelError      = "error"

	// Warning: this is the different convention than Karpenter core
	// Core: empty = unknown
	// This: "unknown" = unknown, empty = no error, which allows the labeling of duration metrics with error
	MetricLabelErrorUnknown = "unknown"
	MetricLabelErrorNone    = ""
)

// getLabelsMapForDuration is a convenience func that constructs a map[string]string
// for a prometheus Label map used to compose a duration metric spec
func getLabelsMapForDuration(ctx context.Context, c *CloudProvider, method string, err error) map[string]string {
	return map[string]string{
		metricLabelController: injection.GetControllerName(ctx),
		metricLabelMethod:     method,
		metricLabelProvider:   c.Name(),
		metricLabelError:      getErrorTypeLabelValue(err),
	}
}

// getErrorTypeLabelValue is a convenience func that returns
// a string representation of well-known CloudProvider error types
func getErrorTypeLabelValue(err error) string {
	// TODO
	switch {
	case err == nil:
		return MetricLabelErrorNone
	default:
		return MetricLabelErrorUnknown
	}
}
