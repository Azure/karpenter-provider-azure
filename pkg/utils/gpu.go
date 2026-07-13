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
	_ "embed"
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"go.yaml.in/yaml/v2"
)

// GPU driver versions and image suffixes, kept in sync with AgentBaker's
// GPUContainerImages in parts/common/components.json. These select the
// mcr.microsoft.com/aks/aks-gpu-<type>:<version>-<suffix> image that the node
// bootstrap uses to install the NVIDIA driver.
//
// TODO: Get these from agentbaker.
const (
	// Legacy R470 driver for NCv1 (K80), installed via the "cuda" image.
	Nvidia470CudaDriverVersion = "470.82.01"

	// Pre-LTS CUDA image (aks-gpu-cuda), R580 line. Only NCv1 is routed to the
	// "cuda" image path; modern CUDA SKUs use the LTS image below. Kept for the
	// legacy path and to mirror AgentBaker (see GetGPUDriverType).
	NvidiaCudaDriverVersion = "580.126.09"
	AKSGPUCudaVersionSuffix = "20260430040408"

	// LTS CUDA image (aks-gpu-cuda-lts), R580.159 line. Default for modern CUDA
	// compute SKUs (T4, V100, A100, H100, H200, ...). This is the branch the AKS
	// GPU VHD driver prebake is built against, so scriptless nodes MUST install
	// this version — installing an older CUDA driver on top of the prebaked
	// userspace libraries produces an NVML "Driver/library version mismatch",
	// which breaks the NVIDIA device plugin and hides the GPU from the node.
	NvidiaCudaLTSDriverVersion = "580.159.04"
	AKSGPUCudaLTSVersionSuffix = "20260629214430"

	NvidiaGridDriverVersion = "570.211.01"
	AKSGPUGridVersionSuffix = "20260522192315"

	NvidiaGridV20DriverVersion = "595.58.03"
	AKSGPUGridV20VersionSuffix = "20260609172331"
)

type GPUSKUInfo struct {
	GPU string   `yaml:"gpu"`
	OS  []string `yaml:"os"`
}

type GPUSKUConfig map[string]GPUSKUInfo

var (
	nvidiaEnabledSKUs        = make(map[string]bool)
	marinerNvidiaEnabledSKUs = make(map[string]bool)
	amdEnabledSKUs           = make(map[string]bool)
	allGPUSKUs               = make(map[string]string)   // sku -> manufacturer ("nvidia", "amd", etc.)
	gpuSKUOSSupport          = make(map[string][]string) // sku -> supported OS list
)

//go:embed supported-gpus.yaml
var configFile []byte

func init() {
	readGPUSKUConfig()
}

func readGPUSKUConfig() {
	var gpuSKUConfig GPUSKUConfig

	err := yaml.Unmarshal(configFile, &gpuSKUConfig)
	if err != nil {
		panic(err)
	}

	for sku, info := range gpuSKUConfig {
		allGPUSKUs[sku] = info.GPU
		gpuSKUOSSupport[sku] = info.OS

		switch info.GPU {
		case v1beta1.ManufacturerNvidia:
			nvidiaEnabledSKUs[sku] = true

			// Check if this SKU supports azurelinux or azurelinux3
			for _, os := range info.OS {
				if os == "azurelinux" || os == "azurelinux3" {
					marinerNvidiaEnabledSKUs[sku] = true
					break
				}
			}
		case v1beta1.ManufacturerAMD:
			amdEnabledSKUs[sku] = true
		}
	}
}

func GetAKSGPUImageSHA(size string) string {
	if UseGridV20Drivers(size) {
		return AKSGPUGridV20VersionSuffix
	}
	if UseGridDrivers(size) {
		return AKSGPUGridVersionSuffix
	}
	// CUDA path (both NCv1 and modern SKUs) uses the LTS image suffix, matching
	// AgentBaker's GetAKSGPUImageSHA.
	return AKSGPUCudaLTSVersionSuffix
}

// IsNvidiaEnabledSKU determines if an VM SKU has nvidia driver support
func IsNvidiaEnabledSKU(vmSize string) bool {
	// Trim the optional _Promo suffix.
	vmSize = strings.ToLower(vmSize)
	vmSize = strings.TrimSuffix(vmSize, "_promo")
	return nvidiaEnabledSKUs[vmSize]
}

// IsNvidiaEnabledSKU determines if an VM SKU has nvidia driver support
func IsMarinerEnabledGPUSKU(vmSize string) bool {
	// Trim the optional _Promo suffix.
	vmSize = strings.ToLower(vmSize)
	vmSize = strings.TrimSuffix(vmSize, "_promo")
	return marinerNvidiaEnabledSKUs[vmSize]
}

// NV series GPUs target graphics workloads vs NC which targets compute.
// they typically use GRID, not CUDA drivers, and will fail to install CUDA drivers.
// NVv1 seems to run with CUDA, NVv5 requires GRID.
// NVv3 is untested on AKS, NVv4 is AMD so n/a, and NVv2 no longer seems to exist (?).
func GetGPUDriverVersion(size string) string {
	if UseGridV20Drivers(size) {
		return NvidiaGridV20DriverVersion
	}
	if UseGridDrivers(size) {
		return NvidiaGridDriverVersion
	}
	if isStandardNCv1(size) {
		return Nvidia470CudaDriverVersion
	}
	return NvidiaCudaLTSDriverVersion
}

// GetGPUDriverType returns the type of GPU driver for given VM SKU ("grid-v20", "grid", "cuda-lts", or "cuda").
// This value becomes NVIDIA_GPU_DRIVER_TYPE at provision time and selects the
// mcr.microsoft.com/aks/aks-gpu-<type> image. Modern CUDA compute SKUs (T4, V100, A100,
// H100, H200, ...) use the R580 LTS image (aks-gpu-cuda-lts) — the branch the GPU VHD
// prebake is built against — while legacy NCv1 (K80) keeps the "cuda" path with its
// pinned R470 driver. Kept in sync with AgentBaker's GetGPUDriverType.
func GetGPUDriverType(size string) string {
	if UseGridV20Drivers(size) {
		return "grid-v20"
	}
	if UseGridDrivers(size) {
		return "grid"
	}
	if isStandardNCv1(size) {
		return "cuda"
	}
	return "cuda-lts"
}

func isStandardNCv1(size string) bool {
	tmp := strings.ToLower(size)
	return strings.HasPrefix(tmp, "standard_nc") && !strings.Contains(tmp, "_v")
}

func UseGridDrivers(size string) bool {
	return ConvergedGPUDriverSizes[strings.ToLower(size)]
}

func UseGridV20Drivers(size string) bool {
	return rtxPro6000GPUDriverSizes[strings.ToLower(size)]
}

/* ConvergedGPUDriverSizes : these sizes use a "converged" driver to support both cuda/grid workloads.
how do you figure this out? ask HPC or find out by trial and error.
installing vanilla cuda drivers will fail to install with opaque errors.
see https://github.com/Azure/azhpc-extensions/blob/daaefd78df6f27012caf30f3b54c3bd6dc437652/NvidiaGPU/resources.json
*/
//nolint:gochecknoglobals
var ConvergedGPUDriverSizes = map[string]bool{
	"standard_nv6ads_a10_v5":   true,
	"standard_nv12ads_a10_v5":  true,
	"standard_nv18ads_a10_v5":  true,
	"standard_nv36ads_a10_v5":  true,
	"standard_nv72ads_a10_v5":  true,
	"standard_nv36adms_a10_v5": true,
	"standard_nc8ads_a10_v4":   true,
	"standard_nc16ads_a10_v4":  true,
	"standard_nc32ads_a10_v4":  true,
}

/* rtxPro6000GPUDriverSizes : NC_RTXPRO6000BSE_v6 (RTX PRO 6000 Blackwell Server
Edition) SKUs require the GRID v20 (595.x) driver, published as the
aks-gpu-grid-v20 image. All other GRID SKUs continue to use aks-gpu-grid.
*/
//nolint:gochecknoglobals
var rtxPro6000GPUDriverSizes = map[string]bool{
	"standard_nc24lds_xl_rtxpro6000bse_v6":  true,
	"standard_nc36ds_xl_rtxpro6000bse_v6":   true,
	"standard_nc36lds_xl_rtxpro6000bse_v6":  true,
	"standard_nc72ds_xl_rtxpro6000bse_v6":   true,
	"standard_nc72lds_xl_rtxpro6000bse_v6":  true,
	"standard_nc144ds_xl_rtxpro6000bse_v6":  true,
	"standard_nc144lds_xl_rtxpro6000bse_v6": true,
	"standard_nc288ds_xl_rtxpro6000bse_v6":  true,
	"standard_nc288lds_xl_rtxpro6000bse_v6": true,
}

// normalizeVMSize applies standard normalization: lowercase and trim _promo suffix
func normalizeVMSize(vmSize string) string {
	vmSize = strings.ToLower(vmSize)
	vmSize = strings.TrimSuffix(vmSize, "_promo")
	return vmSize
}

// IsGPUSKU determines if a VM SKU is a known GPU SKU (any vendor: nvidia, amd, etc.)
func IsGPUSKU(vmSize string) bool {
	vmSize = normalizeVMSize(vmSize)
	_, ok := allGPUSKUs[vmSize]
	return ok
}

// IsAMDEnabledSKU determines if a VM SKU is an AMD GPU SKU
func IsAMDEnabledSKU(vmSize string) bool {
	vmSize = normalizeVMSize(vmSize)
	return amdEnabledSKUs[vmSize]
}

// GetGPUManufacturer returns the GPU manufacturer for a VM SKU ("nvidia", "amd", or "")
func GetGPUManufacturer(vmSize string) string {
	vmSize = normalizeVMSize(vmSize)
	return allGPUSKUs[vmSize]
}

// IsDriverInstallSupported returns true if the system knows how to install
// GPU drivers for this VM SKU. Currently all NVIDIA SKUs have driver installation
// support, while AMD SKUs do not. This is the single abstraction point for this
// decision — when AMD driver support is added, only this function needs to change.
func IsDriverInstallSupported(vmSize string) bool {
	return IsNvidiaEnabledSKU(vmSize)
}

// IsGPUSKUSupportedOnOS checks if a GPU SKU supports a given OS identifier (e.g., "ubuntu", "azurelinux", "azurelinux3")
func IsGPUSKUSupportedOnOS(vmSize string, osName string) bool {
	vmSize = normalizeVMSize(vmSize)
	osList, ok := gpuSKUOSSupport[vmSize]
	if !ok {
		return false
	}
	for _, os := range osList {
		if os == osName {
			return true
		}
	}
	return false
}
