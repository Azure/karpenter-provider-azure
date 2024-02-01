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

package instancetype

import (
	"context"
	"fmt"
	"math"

	"github.com/Azure/skewer"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"knative.dev/pkg/ptr"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"

	"github.com/aws/karpenter-core/pkg/utils/resources"
)

const (
	MemoryAvailable        = "memory.available"
	DefaultMemoryAvailable = "750Mi"
)

var (
	// reservedMemoryTaxGi denotes the tax brackets for memory in Gi.
	reservedMemoryTaxGi = TaxBrackets{
		{
			UpperBound: 4,
			Rate:       .25,
		},
		{
			UpperBound: 8,
			Rate:       .20,
		},
		{
			UpperBound: 16,
			Rate:       .10,
		},
		{
			UpperBound: 128,
			Rate:       .06,
		},
		{
			UpperBound: math.MaxFloat64,
			Rate:       .02,
		},
	}

	//reservedCPUTaxVCPU denotes the tax brackets for Virtual CPU cores.
	reservedCPUTaxVCPU = TaxBrackets{
		{
			UpperBound: 1,
			Rate:       .06,
		},
		{
			UpperBound: 2,
			Rate:       .04,
		},
		{
			UpperBound: 4,
			Rate:       .02,
		},
		{
			UpperBound: math.MaxFloat64,
			Rate:       .01,
		},
	}
)

// TaxBrackets implements a simple bracketed tax structure.
type TaxBrackets []struct {
	// UpperBound is the largest value this bracket is applied to.
	// The first bracket's lower bound is always 0.
	UpperBound float64

	// Rate is the percent rate of tax expressed as a float i.e. .5 for 50%.
	Rate float64
}

// Calculate expects Memory in Gi and CPU in cores.
func (t TaxBrackets) Calculate(amount float64) float64 {
	var tax, lower float64

	for _, bracket := range t {
		if lower > amount {
			continue
		}
		upper := bracket.UpperBound
		if upper > amount {
			upper = amount
		}
		tax += (upper - lower) * bracket.Rate
		lower = bracket.UpperBound
	}

	return tax
}

func NewInstanceType(ctx context.Context, sku *skewer.SKU, vmsize *skewer.VMSizeType, kc *corev1beta1.KubeletConfiguration, region string,
	offerings cloudprovider.Offerings, nodeClass *v1alpha2.AKSNodeClass, architecture string) *cloudprovider.InstanceType {
	return &cloudprovider.InstanceType{
		Name:         sku.GetName(),
		Requirements: computeRequirements(sku, vmsize, architecture, offerings, region),
		Offerings:    offerings,
		Capacity:     computeCapacity(ctx, sku, kc, nodeClass),
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved:      KubeReservedResources(lo.Must(sku.VCPU()), lo.Must(sku.Memory())),
			SystemReserved:    SystemReservedResources(),
			EvictionThreshold: EvictionThreshold(),
		},
	}
}

func computeRequirements(sku *skewer.SKU, vmsize *skewer.VMSizeType, architecture string,
	offerings cloudprovider.Offerings, region string) scheduling.Requirements {
	requirements := scheduling.NewRequirements(
		// Well Known Upstream
		scheduling.NewRequirement(v1.LabelInstanceTypeStable, v1.NodeSelectorOpIn, sku.GetName()),
		scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, getArchitecture(architecture)),
		scheduling.NewRequirement(v1.LabelOSStable, v1.NodeSelectorOpIn, string(v1.Linux)),
		scheduling.NewRequirement(
			v1.LabelTopologyZone,
			v1.NodeSelectorOpIn,
			lo.Map(offerings.Available(),
				func(o cloudprovider.Offering, _ int) string { return o.Zone })...),
		scheduling.NewRequirement(v1.LabelTopologyRegion, v1.NodeSelectorOpIn, region),

		// Well Known to Karpenter
		scheduling.NewRequirement(
			corev1beta1.CapacityTypeLabelKey,
			v1.NodeSelectorOpIn,
			lo.Map(offerings.Available(), func(o cloudprovider.Offering, _ int) string { return o.CapacityType })...),

		// Well Known to Azure
		scheduling.NewRequirement(v1alpha2.LabelSKUCPU, v1.NodeSelectorOpIn, fmt.Sprint(vcpuCount(sku))),
		scheduling.NewRequirement(v1alpha2.LabelSKUMemory, v1.NodeSelectorOpIn, fmt.Sprint((memoryMiB(sku)))), // in MiB
		scheduling.NewRequirement(v1alpha2.LabelSKUGPUCount, v1.NodeSelectorOpIn, fmt.Sprint(gpuNvidiaCount(sku).Value())),
		scheduling.NewRequirement(v1alpha2.LabelSKUGPUManufacturer, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUGPUName, v1.NodeSelectorOpDoesNotExist),

		// composites
		scheduling.NewRequirement(v1alpha2.LabelSKUName, v1.NodeSelectorOpDoesNotExist),

		// size parts
		scheduling.NewRequirement(v1alpha2.LabelSKUFamily, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUAccelerator, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUVersion, v1.NodeSelectorOpDoesNotExist),

		// SKU capabilities
		scheduling.NewRequirement(v1alpha2.LabelSKUStorageEphemeralOSMaxSize, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUStoragePremiumCapable, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUEncryptionAtHostSupported, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUAcceleratedNetworking, v1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpDoesNotExist),
		// all additive feature initialized elsewhere
	)

	// composites
	requirements[v1alpha2.LabelSKUName].Insert(sku.GetName())

	// size parts
	requirements[v1alpha2.LabelSKUFamily].Insert(vmsize.Family)

	// everything from additive features
	for _, featureLabel := range v1alpha2.SkuFeatureToLabel {
		requirements.Add(scheduling.NewRequirement(featureLabel, v1.NodeSelectorOpDoesNotExist))
	}

	setRequirementsAdditiveFeatures(requirements, vmsize)
	setRequirementsStoragePremiumCapable(requirements, sku)
	setRequirementsEncryptionAtHostSupported(requirements, sku)
	setRequirementsEphemeralOSDiskSupported(requirements, sku, vmsize)
	setRequirementsAcceleratedNetworking(requirements, sku)
	setRequirementsHyperVGeneration(requirements, sku)
	setRequirementsGPU(requirements, sku, vmsize)
	setRequirementsAccelerator(requirements, vmsize)
	setRequirementsVersion(requirements, vmsize)

	return requirements
}

func setRequirementsAdditiveFeatures(requirements scheduling.Requirements, vmsize *skewer.VMSizeType) {
	for _, feature := range vmsize.AdditiveFeatures {
		if featureLabel, ok := v1alpha2.SkuFeatureToLabel[feature]; ok {
			requirements[featureLabel].Insert("true")
		}
	}
}

func setRequirementsStoragePremiumCapable(requirements scheduling.Requirements, sku *skewer.SKU) {
	if sku.IsPremiumIO() {
		requirements[v1alpha2.LabelSKUStoragePremiumCapable].Insert("true")
	}
}

func setRequirementsEncryptionAtHostSupported(requirements scheduling.Requirements, sku *skewer.SKU) {
	if sku.IsEncryptionAtHostSupported() {
		requirements[v1alpha2.LabelSKUEncryptionAtHostSupported].Insert("true")
	}
}

func setRequirementsEphemeralOSDiskSupported(requirements scheduling.Requirements, sku *skewer.SKU, vmsize *skewer.VMSizeType) {
	if sku.IsEphemeralOSDiskSupported() && vmsize.Series != "Dlds_v5" { // Dlds_v5 does not support ephemeral OS disk, contrary to what it claims
		requirements[v1alpha2.LabelSKUStorageEphemeralOSMaxSize].Insert(fmt.Sprint(MaxEphemeralOSDiskSizeGB(sku)))
	}
}

func setRequirementsAcceleratedNetworking(requirements scheduling.Requirements, sku *skewer.SKU) {
	if sku.IsAcceleratedNetworkingSupported() {
		requirements[v1alpha2.LabelSKUAcceleratedNetworking].Insert("true")
	}
}

func setRequirementsHyperVGeneration(requirements scheduling.Requirements, sku *skewer.SKU) {
	if sku.IsHyperVGen1Supported() {
		requirements[v1alpha2.LabelSKUHyperVGeneration].Insert(v1alpha2.HyperVGenerationV1)
	}
	if sku.IsHyperVGen2Supported() {
		requirements[v1alpha2.LabelSKUHyperVGeneration].Insert(v1alpha2.HyperVGenerationV2)
	}
}

func setRequirementsGPU(requirements scheduling.Requirements, sku *skewer.SKU, vmsize *skewer.VMSizeType) {
	if utils.IsNvidiaEnabledSKU(sku.GetName()) {
		requirements[v1alpha2.LabelSKUGPUManufacturer].Insert(v1alpha2.ManufacturerNvidia)
		if vmsize.AcceleratorType != nil {
			requirements[v1alpha2.LabelSKUGPUName].Insert(*vmsize.AcceleratorType)
		}
	}
}

func setRequirementsAccelerator(requirements scheduling.Requirements, vmsize *skewer.VMSizeType) {
	if vmsize.AcceleratorType != nil {
		requirements[v1alpha2.LabelSKUAccelerator].Insert(*vmsize.AcceleratorType)
	}
}

// setRequirementsVersion sets the SKU version label, dropping "v" prefix and backfilling "1"
func setRequirementsVersion(requirements scheduling.Requirements, vmsize *skewer.VMSizeType) {
	version := "1"
	if vmsize.Version != "" {
		if !(vmsize.Version[0] == 'V' || vmsize.Version[0] == 'v') {
			// should never happen; don't capture in label (won't be available for selection by version)
			return
		}
		version = vmsize.Version[1:]
	}
	requirements[v1alpha2.LabelSKUVersion].Insert(version)
}

func getArchitecture(architecture string) string {
	if value, ok := v1alpha2.AzureToKubeArchitectures[architecture]; ok {
		return value
	}
	return architecture // unrecognized
}

func computeCapacity(ctx context.Context, sku *skewer.SKU, kc *corev1beta1.KubeletConfiguration, nodeClass *v1alpha2.AKSNodeClass) v1.ResourceList {
	return v1.ResourceList{
		v1.ResourceCPU:                    *cpu(sku),
		v1.ResourceMemory:                 *memory(ctx, sku),
		v1.ResourceEphemeralStorage:       *ephemeralStorage(nodeClass),
		v1.ResourcePods:                   *pods(sku, kc),
		v1.ResourceName("nvidia.com/gpu"): *gpuNvidiaCount(sku),
	}
}

// gpuNvidiaCount returns the number of Nvidia GPUs in the SKU. Currently nvidia is the only gpu manufacturer we support.
func gpuNvidiaCount(sku *skewer.SKU) *resource.Quantity {
	count, err := sku.GPU()
	if err != nil || !utils.IsNvidiaEnabledSKU(sku.GetName()) {
		count = 0
	}
	return resources.Quantity(fmt.Sprint(count))
}

func vcpuCount(sku *skewer.SKU) int64 {
	return lo.Must(sku.VCPU())
}

func cpu(sku *skewer.SKU) *resource.Quantity {
	return resources.Quantity(fmt.Sprint(vcpuCount(sku)))
}

func memoryGiB(sku *skewer.SKU) float64 {
	return lo.Must(sku.Memory()) // contrary to "MemoryGB" capability name, it is in GiB (!)
}

func memoryMiB(sku *skewer.SKU) int64 {
	return int64(memoryGiB(sku) * 1024)
}

func memory(ctx context.Context, sku *skewer.SKU) *resource.Quantity {
	memory := resources.Quantity(fmt.Sprintf("%dGi", int64(memoryGiB(sku))))
	// Account for VM overhead in calculation
	memory.Sub(resource.MustParse(fmt.Sprintf("%dMi", int64(math.Ceil(
		float64(memory.Value())*options.FromContext(ctx).VMMemoryOverheadPercent/1024/1024)))))
	return memory
}

func ephemeralStorage(nodeClass *v1alpha2.AKSNodeClass) *resource.Quantity {
	return resource.NewScaledQuantity(int64(*nodeClass.Spec.OSDiskSizeGB), resource.Giga)
}

func pods(sku *skewer.SKU, kc *corev1beta1.KubeletConfiguration) *resource.Quantity {
	// TODO: fine-tune pods calc
	var count int64
	switch {
	case kc != nil && kc.MaxPods != nil:
		count = int64(ptr.Int32Value(kc.MaxPods))
	default:
		count = 110
	}
	if kc != nil && ptr.Int32Value(kc.PodsPerCore) > 0 {
		count = lo.Min([]int64{int64(ptr.Int32Value(kc.PodsPerCore)) * cpu(sku).Value(), count})
	}
	return resources.Quantity(fmt.Sprint(count))
}

func SystemReservedResources() v1.ResourceList {
	// AKS does not set system-reserved values and only CPU and memory are considered
	// https://learn.microsoft.com/en-us/azure/aks/concepts-clusters-workloads#resource-reservations
	return v1.ResourceList{
		v1.ResourceCPU:    resource.Quantity{},
		v1.ResourceMemory: resource.Quantity{},
	}
}

func KubeReservedResources(vcpus int64, memoryGib float64) v1.ResourceList {
	reservedMemoryMi := int64(1024 * reservedMemoryTaxGi.Calculate(memoryGib))
	reservedCPUMilli := int64(1000 * reservedCPUTaxVCPU.Calculate(float64(vcpus)))

	resources := v1.ResourceList{
		v1.ResourceCPU:    *resource.NewScaledQuantity(reservedCPUMilli, resource.Milli),
		v1.ResourceMemory: *resource.NewQuantity(reservedMemoryMi*1024*1024, resource.BinarySI),
	}

	return resources
}

func EvictionThreshold() v1.ResourceList {
	return v1.ResourceList{
		v1.ResourceMemory: resource.MustParse(DefaultMemoryAvailable),
	}
}
