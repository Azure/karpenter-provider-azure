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

	. "github.com/onsi/gomega"
)

func TestGetAKSGPUImageSHA(t *testing.T) {
	tests := []struct {
		name          string
		size          string
		gpuDriverSha  string
		gpuDriverType string
	}{
		{"GRID Driver - NC Series v4", "standard_nc8ads_a10_v4", AKSGPUGridVersionSuffix, "grid"},
		{"Cuda Driver - NV Series", "standard_nv6", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NC Series", "standard_nc6s_v3", AKSGPUCudaVersionSuffix, "cuda"},
		{"GRID Driver - NV Series v5", "standard_nv6ads_a10_v5", AKSGPUGridVersionSuffix, "grid"},
		{"Unknown SKU", "unknown_sku", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NC Series v2", "standard_nc6s_v2", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NV Series v3", "standard_nv12s_v3", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NC A100", "standard_nc24ads_a100_v4", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - H100", "standard_nc40ads_h100_v5", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - H200", "standard_nd96isr_h200_v5", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - T4", "standard_nc4as_t4_v3", AKSGPUCudaVersionSuffix, "cuda"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(GetAKSGPUImageSHA(test.size)).To(Equal(test.gpuDriverSha), "Failed for size: %s", test.size)
			g.Expect(GetGPUDriverType(test.size)).To(Equal(test.gpuDriverType), "Failed for size: %s", test.size)
		})
	}
}

func TestGetGPUDriverVersion(t *testing.T) {
	tests := []struct {
		name   string
		size   string
		output string
	}{
		{"GRID Driver - NV Series v5", "standard_nv6ads_a10_v5", NvidiaGridDriverVersion},
		{"CUDA Driver - NC Series v1", "standard_nc6", Nvidia470CudaDriverVersion},
		{"CUDA Driver - NC Series v2", "standard_nc6s_v2", NvidiaCudaDriverVersion},
		{"Unknown SKU", "unknown_sku", NvidiaCudaDriverVersion},
		{"CUDA Driver - NC Series v3", "standard_nc6s_v3", NvidiaCudaDriverVersion},
		{"GRID Driver - A10", "standard_nc8ads_a10_v4", NvidiaGridDriverVersion},
		{"CUDA Driver - H100", "standard_nd96isr_h100_v5", NvidiaCudaDriverVersion},
		{"CUDA Driver - H200", "standard_nd96is_h200_v5", NvidiaCudaDriverVersion},
		{"CUDA Driver - ND v2", "standard_nd40rs_v2", NvidiaCudaDriverVersion},
		{"CUDA Driver - ND v3", "standard_nd40s_v3", NvidiaCudaDriverVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := GetGPUDriverVersion(test.size)
			g.Expect(result).To(Equal(test.output), "Failed for size: %s", test.size)
		})
	}
}

func TestIsNvidiaEnabledSKU(t *testing.T) {
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
		{"Valid SKU - H100", "standard_nc40ads_h100_v5", true},
		{"Valid SKU - H200", "standard_nd96isr_h200_v5", true},
		{"Valid SKU - GB200", "standard_nd128isr_ndr_gb200_v6", true},
		{"AMD GPU SKU", "standard_nv4ads_v710_v5", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsNvidiaEnabledSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsMarinerEnabledGPUSKU(t *testing.T) {
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
		{"SKU without azurelinux support", "standard_nc6", false},
		{"ND v2 with azurelinux", "standard_nd40rs_v2", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsMarinerEnabledGPUSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsAMDGPUEnabledSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Valid AMD GPU SKU - V710", "standard_nv4ads_v710_v5", true},
		{"Valid AMD GPU SKU - MI300X", "standard_nd96isr_mi300x_v5", true},
		{"Nvidia GPU SKU", "standard_nc6s_v3", false},
		{"Non-GPU SKU", "standard_d2_v2", false},
		{"Empty SKU", "", false},
		{"Non-Existent SKU", "non_existent_sku", false},
		{"Another AMD GPU", "standard_nv24ads_v710_v5", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsAMDGPUEnabledSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsGPUEnabledSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Nvidia GPU SKU", "standard_nc6s_v3", true},
		{"AMD GPU SKU", "standard_nv4ads_v710_v5", true},
		{"Non-GPU SKU", "standard_d2_v2", false},
		{"Empty SKU", "", false},
		{"H100 SKU", "standard_nc40ads_h100_v5", true},
		{"MI300X SKU", "standard_nd96is_mi300x_v5", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsGPUEnabledSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsAzureLinux3EnabledGPUSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"Valid AzureLinux3 SKU - T4", "standard_nc4as_t4_v3", true},
		{"Valid AzureLinux3 SKU - NC v3", "standard_nc6s_v3", true},
		{"Valid AzureLinux3 SKU - A100", "standard_nc24ads_a100_v4", true},
		{"Valid AzureLinux3 SKU - H100", "standard_nc40ads_h100_v5", true},
		{"Valid AzureLinux3 SKU - H200", "standard_nd96isr_h200_v5", true},
		{"SKU without azurelinux3 support", "standard_nc6", false},
		{"Non-Existent SKU", "non_existent_sku", false},
		{"Empty SKU", "", false},
		{"ND v3 with azurelinux3", "standard_nd40s_v3", true},
		{"ND v2 with azurelinux3", "standard_nd40rs_v2", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsAzureLinux3EnabledGPUSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}
