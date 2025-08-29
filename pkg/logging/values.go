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

// This file contains structured logging values, that ensure ensure use of consistent keys across our
// logs, along with helper functions to assist with prometheus metrics.
// While adhoc logging fields make sense in certain cases, ones with common reuse should be defined hre.
package logging

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// LogValue represents a key-value pair for logging with a known key
type LogValue struct {
	key   string
	value any
}

// Key returns the logging key
func (m LogValue) Key() string {
	return m.key
}

// Value returns the logging value
func (m LogValue) Value() any {
	return m.value
}

// Constructor functions for each metric value type with known keys

func VMName(value string) LogValue {
	return LogValue{key: "vmName", value: value}
}

func Location(value string) LogValue {
	return LogValue{key: "location", value: value}
}

func Zone(value string) LogValue {
	return LogValue{key: "zone", value: value}
}

func InstanceType(value string) LogValue {
	return LogValue{key: "instance-type", value: value}
}

func CapacityType(value string) LogValue {
	return LogValue{key: "capacity-type", value: value}
}

func UseSIG(value bool) LogValue {
	return LogValue{key: "useSIG", value: value}
}

func ImageFamily(value *string) LogValue {
	return LogValue{key: "imageFamily", value: value}
}

func FIPSMode(value *v1beta1.FIPSMode) LogValue {
	return LogValue{key: "fipsMode", value: value}
}

func ImageID(value string) LogValue {
	return LogValue{key: "imageID", value: value}
}

func SubnetID(value string) LogValue {
	return LogValue{key: "subnetID", value: value}
}

func OSDiskSizeGB(value *int32) LogValue {
	return LogValue{key: "osDiskSizeGB", value: value}
}

func StorageProfileIsEphemeral(value bool) LogValue {
	return LogValue{key: "storageProfileIsEphemeral", value: value}
}

func ProvisionMode(value string) LogValue {
	return LogValue{key: "provisionMode", value: value}
}

func Error(value error) LogValue {
	return LogValue{key: "error", value: value}
}

// Helper function to convert a slice of LogValues to their key-value pairs
func ValuesToKeyValuePairs(values ...LogValue) []any {
	var pairs []any
	for _, v := range values {
		pairs = append(pairs, v.Key(), v.Value())
	}
	return pairs
}
