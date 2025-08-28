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

package logvalues

import (
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// MetricValue represents a value that knows its own key for logging
type MetricValue interface {
	ToKeyValuePair() (string, any)
}

// Individual value types that can be reused across metrics
type VMName string

func (v VMName) ToKeyValuePair() (string, any) { return "vmName", v }

type Location string

func (v Location) ToKeyValuePair() (string, any) { return "location", v }

type Zone string

func (v Zone) ToKeyValuePair() (string, any) { return "zone", v }

type InstanceType string

func (v InstanceType) ToKeyValuePair() (string, any) { return "instance-type", v }

type CapacityType string

func (v CapacityType) ToKeyValuePair() (string, any) { return "capacity-type", v }

type UseSIG bool

func (v UseSIG) ToKeyValuePair() (string, any) { return "useSIG", v }

// ImageFamilyWrapper wraps *string because Go doesn't allow methods on pointer type aliases
type ImageFamilyWrapper struct {
	Value *string
}

func (v ImageFamilyWrapper) ToKeyValuePair() (string, any) { return "imageFamily", v.Value }

// ImageFamily constructor function for consistent usage syntax
func ImageFamily(value *string) ImageFamilyWrapper {
	return ImageFamilyWrapper{Value: value}
}

// FipsModeWrapper wraps *v1beta1.FIPSMode because Go doesn't allow methods on pointer type aliases
type FipsModeWrapper struct {
	Value *v1beta1.FIPSMode
}

func (v FipsModeWrapper) ToKeyValuePair() (string, any) { return "fipsMode", v.Value }

// FipsMode constructor function for consistent usage syntax
func FipsMode(value *v1beta1.FIPSMode) FipsModeWrapper {
	return FipsModeWrapper{Value: value}
}

type ImageID string

func (v ImageID) ToKeyValuePair() (string, any) { return "imageID", v }

type SubnetID string

func (v SubnetID) ToKeyValuePair() (string, any) { return "subnetID", v }

// OSDiskSizeGBWrapper wraps *int32 because Go doesn't allow methods on pointer type aliases
type OSDiskSizeGBWrapper struct {
	Value *int32
}

func (v OSDiskSizeGBWrapper) ToKeyValuePair() (string, any) { return "osDiskSizeGB", v.Value }

// OSDiskSizeGB constructor function for consistent usage syntax
func OSDiskSizeGB(value *int32) OSDiskSizeGBWrapper {
	return OSDiskSizeGBWrapper{Value: value}
}

type StorageProfileIsEphemeral bool

func (v StorageProfileIsEphemeral) ToKeyValuePair() (string, any) {
	return "storageProfileIsEphemeral", v
}

type ProvisionMode string

func (v ProvisionMode) ToKeyValuePair() (string, any) { return "provisionMode", v }

// MetricValues interface for types that can convert themselves to key-value pairs
type MetricValues interface {
	ToKeyValuePairs() []any
}

// Helper function to convert a slice of MetricValue to key-value pairs
func ValuesToKeyValuePairs(values ...MetricValue) []any {
	var pairs []any
	for _, v := range values {
		key, val := v.ToKeyValuePair()
		pairs = append(pairs, key, val)
	}
	return pairs
}
