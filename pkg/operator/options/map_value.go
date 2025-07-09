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

package options

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// MapValue implements flag.Value for parsing a string of key=value pairs into a map[string]string
type MapValue map[string]string

func NewMapValue() MapValue {
	return make(MapValue)
}

func NewMapValueFromMapPtr(value string, m *map[string]string) MapValue {
	*m = make(map[string]string)
	result := MapValue(*m)

	if err := result.Set(value); err != nil {
		panic(fmt.Sprintf("failed to set MapValue from string %q: %s", value, err))
	}
	return result
}

// String returns the string representation of the map
func (m MapValue) String() string {
	if m == nil {
		return ""
	}

	keys := maps.Keys(m)
	sorted := slices.Sorted(keys)

	var pairs []string
	for _, k := range sorted {
		v := m[k]
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, ",")
}

// Set parses a string of the format key1=value1,key2=value2. Empty entries (",,") are ignored.
// Key-only entries, e.g. "key1=" are allowed and result in a map entry with an empty-string value.
func (m MapValue) Set(value string) error {
	for k := range m {
		// Clear existing values to allow re-setting
		delete(m, k)
	}

	// Split by comma and parse each key=value pair
	pairs := strings.Split(value, ",")
	for _, pair := range pairs {
		pairTrimmed := strings.TrimSpace(pair)
		if pairTrimmed == "" {
			continue
		}

		// Split by = to get key and value
		parts := strings.Split(pairTrimmed, "=")
		if len(parts) != 2 {
			return fmt.Errorf("invalid key=value pair: %q", pair)
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		if key == "" {
			return fmt.Errorf("empty key in pair: %q", pair)
		}

		m[key] = val
	}

	return nil
}
