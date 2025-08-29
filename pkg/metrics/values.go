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

// MetricValue represents a key-value pair for logging with a known key
type MetricValue struct {
	key   string
	value any
}

// Key returns the logging key
func (m MetricValue) Key() string {
	return m.key
}

// Value returns the logging value
func (m MetricValue) Value() any {
	return m.value
}

// Constructor functions for each metric value type with known keys

func ImageID(value string) MetricValue {
	// TODO: refactor this key into a sharable consts.
	return MetricValue{key: "imageID", value: value}
}

// Helper function to convert a slice of MetricValues to their key-value pairs
func ValuesToKeyValuePairs(values ...MetricValue) []any {
	var pairs []any
	for _, v := range values {
		pairs = append(pairs, v.Key(), v.Value())
	}
	return pairs
}
