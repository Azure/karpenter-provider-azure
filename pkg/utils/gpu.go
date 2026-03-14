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

	"go.yaml.in/yaml/v2"
)

// TODO: Get these from agentbaker
const (
	Nvidia470CudaDriverVersion = "470.82.01"

	// https://github.com/Azure/AgentBaker/blob/c0e684e5cecebcf61554cc7d2e2d2191972d35ed/parts/common/components.json#L797-L811
	NvidiaCudaDriverVersion = "550.144.03"
	AKSGPUCudaVersionSuffix = "20250328201547"

	NvidiaGridDriverVersion = "550.144.06"
	AKSGPUGridVersionSuffix = "20250512225043"
)

type NvidiaSKUConfig struct {
	NvidiaEnabledSKUFamilies        map[string][]string `yaml:"nvidiaEnabledSKUs"`
	MarinerNvidiaEnabledSKUFamilies map[string][]string `yaml:"marinerNvidiaEnabledSKUs"`
}

var (
	nvidiaEnabledSKUs        = make(map[string]bool)
	marinerNvidiaEnabledSKUs = make(map[string]bool)
)

//go:embed supported-gpus.yaml
var configFile []byte

func init() {
	readNvidiaSKUConfig()
}

func readNvidiaSKUConfig() {
	var nvidiaSKUConfig NvidiaSKUConfig

	err := yaml.Unmarshal(configFile, &nvidiaSKUConfig)
	if err != nil {
		panic(err)
	}
	for _, skus := range nvidiaSKUConfig.NvidiaEnabledSKUFamilies {
		for _, sku := range skus {
			nvidiaEnabledSKUs[sku] = true
		}
	}
	for _, skus := range nvidiaSKUConfig.MarinerNvidiaEnabledSKUFamilies {
		for _, sku := range skus {
			marinerNvidiaEnabledSKUs[sku] = true
		}
	}
}

func GetAKSGPUImageSHA(size string) string {
	if UseGridDrivers(size) {
		return AKSGPUGridVersionSuffix
	}
	return AKSGPUCudaVersionSuffix
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
	if UseGridDrivers(size) {
		return NvidiaGridDriverVersion
	}
	if isStandardNCv1(size) {
		return Nvidia470CudaDriverVersion
	}
	return NvidiaCudaDriverVersion
}

// GetGPUDriverType returns the type of GPU driver for given VM SKU ("grid" or "cuda")
func GetGPUDriverType(size string) string {
	if UseGridDrivers(size) {
		return "grid"
	}
	return "cuda"
}

func isStandardNCv1(size string) bool {
	tmp := strings.ToLower(size)
	return strings.HasPrefix(tmp, "standard_nc") && !strings.Contains(tmp, "_v")
}

func UseGridDrivers(size string) bool {
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
