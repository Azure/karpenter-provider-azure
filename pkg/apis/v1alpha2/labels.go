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

package v1alpha2

import (
	"k8s.io/apimachinery/pkg/util/sets"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func init() {
	corev1beta1.RestrictedLabelDomains = corev1beta1.RestrictedLabelDomains.Insert(RestrictedLabelDomains...)
	corev1beta1.WellKnownLabels = corev1beta1.WellKnownLabels.Insert(
		LabelSKUName,
		LabelSKUFamily,
		LabelSKUVersion,

		LabelSKUCPU,
		LabelSKUMemory,
		LabelSKUAccelerator,

		LabelSKUAcceleratedNetworking,

		LabelSKUStoragePremiumCapable,
		LabelSKUStorageEphemeralOSMaxSize,

		LabelSKUEncryptionAtHostSupported,

		LabelSKUGPUName,
		LabelSKUGPUManufacturer,
		LabelSKUGPUCount,

		AKSLabelCluster,
	)
}

var (
	AzureToKubeArchitectures = map[string]string{
		// TODO: consider using constants like compute.ArchitectureArm64
		"x64":   corev1beta1.ArchitectureAmd64,
		"Arm64": corev1beta1.ArchitectureArm64,
	}
	RestrictedLabelDomains = []string{
		Group,
	}

	RestrictedLabels = sets.New(
		LabelSKUHyperVGeneration,
	)

	AllowUndefinedLabels = func(options scheduling.CompatibilityOptions) scheduling.CompatibilityOptions {
		options.AllowUndefined = corev1beta1.WellKnownLabels.Union(RestrictedLabels)
		return options
	}

	// alternative zone label for Machine (the standard one is protected for AKS nodes)
	AlternativeLabelTopologyZone = Group + "/zone"

	HyperVGenerationV1 = "1"
	HyperVGenerationV2 = "2"
	ManufacturerNvidia = "nvidia"

	LabelSKUName    = Group + "/sku-name"    // Standard_A1_v2
	LabelSKUFamily  = Group + "/sku-family"  // A
	LabelSKUVersion = Group + "/sku-version" // numerical (without v), with 1 backfilled

	LabelSKUCPU         = Group + "/sku-cpu"    // sku.vCPUs
	LabelSKUMemory      = Group + "/sku-memory" // sku.MemoryGB
	LabelSKUAccelerator = Group + "/sku-accelerator"

	// selected capabilities (from additive features in VM size name, or from SKU capabilities)
	LabelSKUAcceleratedNetworking = Group + "/sku-networking-accelerated" // sku.AcceleratedNetworkingEnabled

	LabelSKUStoragePremiumCapable     = Group + "/sku-storage-premium-capable"     // sku.IsPremiumIO
	LabelSKUStorageEphemeralOSMaxSize = Group + "/sku-storage-ephemeralos-maxsize" // calculated as max(sku.CachedDiskBytes, sku.MaxResourceVolumeMB)

	LabelSKUEncryptionAtHostSupported = Group + "/sku-encryptionathost-capable" // sku.EncryptionAtHostSupported

	// GPU labels
	LabelSKUGPUName         = Group + "/sku-gpu-name"         // ie GPU Accelerator type we parse from vmSize
	LabelSKUGPUManufacturer = Group + "/sku-gpu-manufacturer" // ie NVIDIA, AMD, etc
	LabelSKUGPUCount        = Group + "/sku-gpu-count"        // ie 16, 32, etc

	// Internal/restricted labels
	LabelSKUHyperVGeneration = Group + "/sku-hyperv-generation" // sku.HyperVGenerations

	// AKS labels
	AKSLabelDomain = "kubernetes.azure.com"

	AKSLabelCluster = AKSLabelDomain + "/cluster"
)

const (
	Ubuntu2204ImageFamily = "Ubuntu2204"
	AzureLinuxImageFamily = "AzureLinux"
)
