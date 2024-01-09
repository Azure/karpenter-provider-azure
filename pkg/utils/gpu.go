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
	"strings"
)

// TODO: Get these from agentbaker
const (
	Nvidia470CudaDriverVersion = "cuda-470.82.01"
	Nvidia535CudaDriverVersion = "cuda-535.54.03"
	Nvidia535GridDriverVersion = "grid-535.54.03"

	AKSGPUGridSHA = "sha-20ffa2"
	AKSGPUCudaSHA = "sha-ff213d"
)

func GetAKSGPUImageSHA(size string) string {
	if useGridDrivers(size) {
		return AKSGPUGridSHA
	}
	return AKSGPUCudaSHA
}

var (
	/* If a new GPU sku becomes available, add a key to this map, but only if you have a confirmation
	   that we have an agreement with NVIDIA for this specific gpu.
	*/
	NvidiaEnabledSKUs = map[string]bool{
		// M60
		"standard_nv6":      true,
		"standard_nv12":     true,
		"standard_nv12s_v3": true,
		"standard_nv24":     true,
		"standard_nv24s_v3": true,
		"standard_nv24r":    true,
		"standard_nv48s_v3": true,
		// P40
		"standard_nd6s":   true,
		"standard_nd12s":  true,
		"standard_nd24s":  true,
		"standard_nd24rs": true,
		// P100
		"standard_nc6s_v2":   true,
		"standard_nc12s_v2":  true,
		"standard_nc24s_v2":  true,
		"standard_nc24rs_v2": true,
		// V100
		"standard_nc6s_v3":   true,
		"standard_nc12s_v3":  true,
		"standard_nc24s_v3":  true,
		"standard_nc24rs_v3": true,
		"standard_nd40s_v3":  true,
		"standard_nd40rs_v2": true,
		// T4
		"standard_nc4as_t4_v3":  true,
		"standard_nc8as_t4_v3":  true,
		"standard_nc16as_t4_v3": true,
		"standard_nc64as_t4_v3": true,
		// A100 40GB
		"standard_nd96asr_v4":       true,
		"standard_nd112asr_a100_v4": true,
		"standard_nd120asr_a100_v4": true,
		// A100 80GB
		"standard_nd96amsr_a100_v4":  true,
		"standard_nd112amsr_a100_v4": true,
		"standard_nd120amsr_a100_v4": true,
		// A100 PCIE 80GB
		"standard_nc24ads_a100_v4": true,
		"standard_nc48ads_a100_v4": true,
		"standard_nc96ads_a100_v4": true,
		"standard_ncads_a100_v4":   true,
		// A10
		"standard_nc8ads_a10_v4":  true,
		"standard_nc16ads_a10_v4": true,
		"standard_nc32ads_a10_v4": true,
		// A10, GRID only
		"standard_nv6ads_a10_v5":   true,
		"standard_nv12ads_a10_v5":  true,
		"standard_nv18ads_a10_v5":  true,
		"standard_nv36ads_a10_v5":  true,
		"standard_nv36adms_a10_v5": true,
		"standard_nv72ads_a10_v5":  true,
		// A100
		"standard_nd96ams_v4":      true,
		"standard_nd96ams_a100_v4": true,
	}

	// List of GPU SKUs currently enabled and validated for Mariner. Will expand the support
	// to cover other SKUs available in Azure
	MarinerNvidiaEnabledSKUs = map[string]bool{
		// V100
		"standard_nc6s_v3":   true,
		"standard_nc12s_v3":  true,
		"standard_nc24s_v3":  true,
		"standard_nc24rs_v3": true,
		"standard_nd40s_v3":  true,
		"standard_nd40rs_v2": true,
		// T4
		"standard_nc4as_t4_v3":  true,
		"standard_nc8as_t4_v3":  true,
		"standard_nc16as_t4_v3": true,
		"standard_nc64as_t4_v3": true,
	}
)

// IsNvidiaEnabledSKU determines if an VM SKU has nvidia driver support
func IsNvidiaEnabledSKU(vmSize string) bool {
	// Trim the optional _Promo suffix.
	vmSize = strings.ToLower(vmSize)
	vmSize = strings.TrimSuffix(vmSize, "_promo")
	return NvidiaEnabledSKUs[vmSize]
}

// IsNvidiaEnabledSKU determines if an VM SKU has nvidia driver support
func IsMarinerEnabledGPUSKU(vmSize string) bool {
	// Trim the optional _Promo suffix.
	vmSize = strings.ToLower(vmSize)
	vmSize = strings.TrimSuffix(vmSize, "_promo")
	return MarinerNvidiaEnabledSKUs[vmSize]
}

// NV series GPUs target graphics workloads vs NC which targets compute.
// they typically use GRID, not CUDA drivers, and will fail to install CUDA drivers.
// NVv1 seems to run with CUDA, NVv5 requires GRID.
// NVv3 is untested on AKS, NVv4 is AMD so n/a, and NVv2 no longer seems to exist (?).
func GetGPUDriverVersion(size string) string {
	if useGridDrivers(size) {
		return Nvidia535GridDriverVersion
	}
	if isStandardNCv1(size) {
		return Nvidia470CudaDriverVersion
	}
	return Nvidia535CudaDriverVersion
}

func isStandardNCv1(size string) bool {
	tmp := strings.ToLower(size)
	return strings.HasPrefix(tmp, "standard_nc") && !strings.Contains(tmp, "_v")
}

func useGridDrivers(size string) bool {
	return ConvergedGPUDriverSizes[strings.ToLower(size)]
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
