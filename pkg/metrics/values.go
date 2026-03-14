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

import "github.com/Azure/karpenter-provider-azure/pkg/logging"

// Value represents a key-value pair for logging with a known key
type Value struct {
	key   string
	value any
}

// Key returns the logging key
func (m Value) Key() string {
	return m.key
}

// Value returns the logging value
func (m Value) Value() any {
	return m.value
}

// Constructor functions for each metric value type with known keys

func ImageID(value string) Value {
	return Value{key: logging.ImageID, value: value}
}

func ResponseError(value string) Value {
	return Value{key: "responseError", value: value}
}

// Helper function to convert a slice of Values to their key-value pairs
func ValuesToKeyValuePairs(values ...Value) []any {
	var pairs []any
	for _, v := range values {
		pairs = append(pairs, v.Key(), v.Value())
	}
	return pairs
}
