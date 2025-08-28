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

package metricvalues

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

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

func VMName(value string) MetricValue {
	return MetricValue{key: "vmName", value: value}
}

func Location(value string) MetricValue {
	return MetricValue{key: "location", value: value}
}

func Zone(value string) MetricValue {
	return MetricValue{key: "zone", value: value}
}

func InstanceType(value string) MetricValue {
	return MetricValue{key: "instance-type", value: value}
}

func CapacityType(value string) MetricValue {
	return MetricValue{key: "capacity-type", value: value}
}

func UseSIG(value bool) MetricValue {
	return MetricValue{key: "useSIG", value: value}
}

func ImageFamily(value *string) MetricValue {
	return MetricValue{key: "imageFamily", value: value}
}

func FipsMode(value *v1beta1.FIPSMode) MetricValue {
	return MetricValue{key: "fipsMode", value: value}
}

func ImageID(value string) MetricValue {
	return MetricValue{key: "imageID", value: value}
}

func SubnetID(value string) MetricValue {
	return MetricValue{key: "subnetID", value: value}
}

func OSDiskSizeGB(value *int32) MetricValue {
	return MetricValue{key: "osDiskSizeGB", value: value}
}

func StorageProfileIsEphemeral(value bool) MetricValue {
	return MetricValue{key: "storageProfileIsEphemeral", value: value}
}

func ProvisionMode(value string) MetricValue {
	return MetricValue{key: "provisionMode", value: value}
}

func Error(value error) MetricValue {
	return MetricValue{key: "error", value: value}
}

// Helper function to convert a slice of MetricValues to their key-value pairs
func ValuesToKeyValuePairs(values ...MetricValue) []any {
	var pairs []any
	for _, v := range values {
		pairs = append(pairs, v.Key(), v.Value())
	}
	return pairs
}
