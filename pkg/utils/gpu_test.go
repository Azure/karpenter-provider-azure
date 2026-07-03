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
		{"GRID v20 Driver - RTX PRO 6000 BSE ds", "standard_nc128ds_xl_rtxpro6000bse_v6", AKSGPUGridV20VersionSuffix, "grid-v20"},
		{"GRID v20 Driver - RTX PRO 6000 BSE", "standard_nc128lds_xl_rtxpro6000bse_v6", AKSGPUGridV20VersionSuffix, "grid-v20"},
		{"GRID v20 Driver - RTX PRO 6000 BSE mixed case", "Standard_NC320lds_xl_RTXPRO6000BSE_v6", AKSGPUGridV20VersionSuffix, "grid-v20"},
		{"Cuda Driver - NV Series", "standard_nv6", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NC Series", "standard_nc6s_v3", AKSGPUCudaVersionSuffix, "cuda"},
		{"GRID Driver - NV Series v5", "standard_nv6ads_a10_v5", AKSGPUGridVersionSuffix, "grid"},
		{"Unknown SKU", "unknown_sku", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NC Series v2", "standard_nc6s_v2", AKSGPUCudaVersionSuffix, "cuda"},
		{"CUDA Driver - NV Series v3", "standard_nv12s_v3", AKSGPUCudaVersionSuffix, "cuda"},
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
		{"GRID v20 Driver - RTX PRO 6000 BSE ds", "standard_nc128ds_xl_rtxpro6000bse_v6", NvidiaGridV20DriverVersion},
		{"GRID v20 Driver - RTX PRO 6000 BSE", "standard_nc128lds_xl_rtxpro6000bse_v6", NvidiaGridV20DriverVersion},
		{"GRID v20 Driver - RTX PRO 6000 BSE mixed case", "Standard_NC320lds_xl_RTXPRO6000BSE_v6", NvidiaGridV20DriverVersion},
		{"CUDA Driver - NC Series v1", "standard_nc6s", Nvidia470CudaDriverVersion},
		{"CUDA Driver - NC Series v2", "standard_nc6s_v2", NvidiaCudaDriverVersion},
		{"Unknown SKU", "unknown_sku", NvidiaCudaDriverVersion},
		{"CUDA Driver - NC Series v3", "standard_nc6s_v3", NvidiaCudaDriverVersion},
		{"GRID Driver - A10", "standard_nc8ads_a10_v4", NvidiaGridDriverVersion},
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
		{"Valid SKU - RTX PRO 6000 BSE ds", "standard_nc128ds_xl_rtxpro6000bse_v6", true},
		{"Valid SKU - RTX PRO 6000 BSE", "standard_nc128lds_xl_rtxpro6000bse_v6", true},
		{"Valid SKU - RTX PRO 6000 BSE mixed case", "Standard_NC320lds_xl_RTXPRO6000BSE_v6", true},
		{"Empty SKU", "", false},
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsMarinerEnabledGPUSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsGPUSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"NVIDIA SKU - NC Series", "standard_nc6s_v3", true},
		{"NVIDIA SKU with Promo", "standard_nc6_promo", true},
		{"AMD SKU - V710", "standard_nv4ads_v710_v5", true},
		{"AMD SKU - MI300X", "standard_nd96isr_mi300x_v5", true},
		{"Non-GPU SKU", "standard_d2_v2", false},
		{"Empty SKU", "", false},
		{"Non-Existent SKU", "non_existent_sku", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsGPUSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsAMDEnabledSKU(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"AMD SKU - V710", "standard_nv4ads_v710_v5", true},
		{"AMD SKU - V710 large", "standard_nv28adms_v710_v5", true},
		{"AMD SKU - MI300X", "standard_nd96isr_mi300x_v5", true},
		{"AMD SKU - MI300X no RDMA", "standard_nd96is_mi300x_v5", true},
		{"NVIDIA SKU - not AMD", "standard_nc6s_v3", false},
		{"Non-GPU SKU", "standard_d2_v2", false},
		{"Empty SKU", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsAMDEnabledSKU(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestGetGPUManufacturer(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output string
	}{
		{"NVIDIA SKU", "standard_nc6s_v3", "nvidia"},
		{"NVIDIA SKU with Promo", "standard_nc6_promo", "nvidia"},
		{"NVIDIA SKU - RTX PRO 6000 BSE ds", "standard_nc128ds_xl_rtxpro6000bse_v6", "nvidia"},
		{"NVIDIA SKU - RTX PRO 6000 BSE", "standard_nc128lds_xl_rtxpro6000bse_v6", "nvidia"},
		{"AMD SKU - V710", "standard_nv4ads_v710_v5", "amd"},
		{"AMD SKU - MI300X", "standard_nd96isr_mi300x_v5", "amd"},
		{"Non-GPU SKU", "standard_d2_v2", ""},
		{"Empty SKU", "", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := GetGPUManufacturer(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}

func TestIsGPUSKUSupportedOnOS(t *testing.T) {
	tests := []struct {
		name   string
		vmSize string
		osName string
		output bool
	}{
		{"NVIDIA on Ubuntu", "standard_nc6s_v3", "ubuntu", true},
		{"NVIDIA on AzureLinux", "standard_nc6s_v3", "azurelinux", true},
		{"NVIDIA Ubuntu-only on AzureLinux", "standard_nc6", "azurelinux", false},
		{"NVIDIA Ubuntu-only on Ubuntu", "standard_nc6", "ubuntu", true},
		{"AMD on Ubuntu", "standard_nv4ads_v710_v5", "ubuntu", true},
		{"AMD on AzureLinux", "standard_nv4ads_v710_v5", "azurelinux", false},
		{"RTX PRO 6000 BSE ds on Ubuntu", "standard_nc128ds_xl_rtxpro6000bse_v6", "ubuntu", true},
		{"RTX PRO 6000 BSE on Ubuntu", "standard_nc128lds_xl_rtxpro6000bse_v6", "ubuntu", true},
		{"RTX PRO 6000 BSE on AzureLinux", "standard_nc128lds_xl_rtxpro6000bse_v6", "azurelinux", false},
		{"Non-GPU SKU", "standard_d2_v2", "ubuntu", false},
		{"Empty SKU", "", "ubuntu", false},
		{"Unknown OS", "standard_nc6s_v3", "windows_server_2025", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsGPUSKUSupportedOnOS(test.vmSize, test.osName)
			g.Expect(result).To(Equal(test.output), "Failed for vmSize: %s, os: %s", test.vmSize, test.osName)
		})
	}
}

func TestIsDriverInstallSupported(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output bool
	}{
		{"NVIDIA SKU - has support", "standard_nc6s_v3", true},
		{"NVIDIA SKU with Promo - has support", "standard_nc6s_v2_promo", true},
		{"NVIDIA T4 - has support", "standard_nc4as_t4_v3", true},
		{"NVIDIA A10 converged - has support", "standard_nv6ads_a10_v5", true},
		{"NVIDIA RTX PRO 6000 BSE ds - has support", "standard_nc128ds_xl_rtxpro6000bse_v6", true},
		{"NVIDIA RTX PRO 6000 BSE - has support", "standard_nc128lds_xl_rtxpro6000bse_v6", true},
		{"AMD SKU V710 - no support", "standard_nv4ads_v710_v5", false},
		{"AMD SKU MI300X - no support", "standard_nd96isr_mi300x_v5", false},
		{"Non-GPU SKU - no support", "standard_d2_v2", false},
		{"Empty SKU - no support", "", false},
		{"Unknown SKU - no support", "non_existent_sku", false},
		{"Case insensitive - has support", "Standard_NC6s_V3", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsDriverInstallSupported(test.input)
			g.Expect(result).To(Equal(test.output), "Failed for input: %s", test.input)
		})
	}
}
