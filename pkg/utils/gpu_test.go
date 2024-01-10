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

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAKSGPUImageSHA(t *testing.T) {
	assert := assert.New(t)
	tests := []struct {
		name   string
		size   string
		output string
	}{
		{"GRID Driver - NC Series v4", "standard_nc8ads_a10_v4", AKSGPUGridSHA},
		{"Cuda Driver - NV Series", "standard_nv6", AKSGPUCudaSHA},
		{"CUDA Driver - NC Series", "standard_nc6s_v3", AKSGPUCudaSHA},
		{"GRID Driver - NV Series v5", "standard_nv6ads_a10_v5", AKSGPUGridSHA},
		{"Unknown SKU", "unknown_sku", AKSGPUCudaSHA},
		{"CUDA Driver - NC Series v2", "standard_nc6s_v2", AKSGPUCudaSHA},
		{"CUDA Driver - NV Series v3", "standard_nv12s_v3", AKSGPUCudaSHA},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetAKSGPUImageSHA(test.size)
			assert.Equal(test.output, result, "Failed for size: %s", test.size)
		})
	}
}

func TestGetGPUDriverVersion(t *testing.T) {
	assert := assert.New(t)
	tests := []struct {
		name   string
		size   string
		output string
	}{
		{"GRID Driver - NV Series v5", "standard_nv6ads_a10_v5", Nvidia535GridDriverVersion},
		{"CUDA Driver - NC Series v1", "standard_nc6s", Nvidia470CudaDriverVersion},
		{"CUDA Driver - NC Series v2", "standard_nc6s_v2", Nvidia535CudaDriverVersion},
		{"Unknown SKU", "unknown_sku", Nvidia535CudaDriverVersion},
		{"CUDA Driver - NC Series v3", "standard_nc6s_v3", Nvidia535CudaDriverVersion},
		{"GRID Driver - A10", "standard_nc8ads_a10_v4", Nvidia535GridDriverVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetGPUDriverVersion(test.size)
			assert.Equal(test.output, result, "Failed for size: %s", test.size)
		})
	}
}

func TestIsNvidiaEnabledSKU(t *testing.T) {
	assert := assert.New(t)
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Valid SKU - NC Series", "standard_nc6s_v3", true},
		{"Valid SKU with Promo", "standard_nc6s_v2_promo", true},
		{"Non-Existent SKU", "non_existent_sku", false},
		{"Valid SKU - NV Series", "standard_nv6", true},
		{"Invalid SKU", "standard_d2_v2", false},
		{"Valid SKU - T4 Series", "standard_nc4as_t4_v3", true},
		{"Empty SKU", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsNvidiaEnabledSKU(test.input)
			assert.Equal(test.output, result, "Failed for input: %s", test.input)
		})
	}
}

func TestIsMarinerEnabledGPUSKU(t *testing.T) {
	assert := assert.New(t)
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Valid Mariner SKU - V100", "standard_nc6s_v3", true},
		{"Valid Mariner SKU with Promo", "standard_nc6s_v3_promo", true},
		{"Non-Existent Mariner SKU", "non_existent_sku", false},
		{"Valid Mariner SKU - T4", "standard_nc4as_t4_v3", true},
		{"Invalid Mariner SKU", "standard_d2_v2", false},
		{"Valid Mariner SKU - ND Series", "standard_nd40s_v3", true},
		{"Empty Mariner SKU", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsMarinerEnabledGPUSKU(test.input)
			assert.Equal(test.output, result, "Failed for input: %s", test.input)
		})
	}
}
