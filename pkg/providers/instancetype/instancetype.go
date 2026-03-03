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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"

	"sigs.k8s.io/karpenter/pkg/utils/resources"
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

func NewInstanceType(
	ctx context.Context,
	sku *skewer.SKU,
	vmsize *skewer.VMSizeType,
	kc *v1beta1.KubeletConfiguration,
	region string,
	offerings cloudprovider.Offerings,
	nodeClass *v1beta1.AKSNodeClass,
	architecture string,
) *cloudprovider.InstanceType {
	return &cloudprovider.InstanceType{
		Name:         sku.GetName(),
		Requirements: computeRequirements(options.FromContext(ctx), sku, vmsize, architecture, offerings, region, nodeClass),
		Offerings:    offerings,
		Capacity:     computeCapacity(ctx, sku, nodeClass),
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved:      KubeReservedResources(lo.Must(sku.VCPU()), lo.Must(sku.Memory())),
			SystemReserved:    SystemReservedResources(),
			EvictionThreshold: EvictionThreshold(),
		},
	}
}

func computeRequirements(
	opts *options.Options,
	sku *skewer.SKU,
	vmsize *skewer.VMSizeType,
	architecture string,
	offerings cloudprovider.Offerings,
	region string,
	nodeClass *v1beta1.AKSNodeClass,
) scheduling.Requirements {
	requirements := scheduling.NewRequirements(
		// Well Known Upstream
		scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, sku.GetName()),
		scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, getArchitecture(architecture)),
		scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, string(corev1.Linux)),
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, lo.Map(offerings.Available(), func(o *cloudprovider.Offering, _ int) string {
			return o.Requirements.Get(corev1.LabelTopologyZone).Any()
		})...),

		scheduling.NewRequirement(corev1.LabelTopologyRegion, corev1.NodeSelectorOpIn, region),

		// Well Known to Karpenter
		scheduling.NewRequirement(karpv1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, lo.Map(offerings.Available(), func(o *cloudprovider.Offering, _ int) string {
			return o.Requirements.Get(karpv1.CapacityTypeLabelKey).Any()
		})...),

		// Well Known to Azure
		scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, fmt.Sprint(vcpuCount(sku))),
		scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, fmt.Sprint((memoryMiB(sku)))), // in MiB
		scheduling.NewRequirement(v1beta1.AKSLabelCPU, corev1.NodeSelectorOpIn, fmt.Sprint(vcpuCount(sku))),      // AKS domain.
		scheduling.NewRequirement(v1beta1.AKSLabelMemory, corev1.NodeSelectorOpIn, fmt.Sprint((memoryMiB(sku)))), // AKS domain.
		scheduling.NewRequirement(v1beta1.LabelSKUGPUCount, corev1.NodeSelectorOpIn, fmt.Sprint(gpuNvidiaCount(sku).Value())),
		scheduling.NewRequirement(v1beta1.LabelSKUGPUManufacturer, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1beta1.LabelSKUGPUName, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1beta1.AKSLabelCluster, corev1.NodeSelectorOpIn, utils.NormalizeClusterResourceGroupNameForLabel(opts.NodeResourceGroup)),
		scheduling.NewRequirement(v1beta1.AKSLabelMode, corev1.NodeSelectorOpIn, v1beta1.ModeSystem, v1beta1.ModeUser),
		scheduling.NewRequirement(v1beta1.AKSLabelScaleSetPriority, corev1.NodeSelectorOpIn, v1beta1.ScaleSetPriorityRegular, v1beta1.ScaleSetPrioritySpot),
		scheduling.NewRequirement(v1beta1.AKSLabelOSSKU, corev1.NodeSelectorOpIn, v1beta1.GetOSSKUFromImageFamily(lo.FromPtr(nodeClass.Spec.ImageFamily))),
		scheduling.NewRequirement(v1beta1.AKSLabelFIPSEnabled, corev1.NodeSelectorOpDoesNotExist), // AKS only sets this label if FIPS is enabled, otherwise it's expected to be empty

		// composites
		scheduling.NewRequirement(v1beta1.LabelSKUName, corev1.NodeSelectorOpDoesNotExist),

		// size parts
		scheduling.NewRequirement(v1beta1.LabelSKUFamily, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1beta1.LabelSKUSeries, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1beta1.LabelSKUVersion, corev1.NodeSelectorOpDoesNotExist),

		// SKU capabilities
		scheduling.NewRequirement(v1beta1.LabelSKUStorageEphemeralOSMaxSize, corev1.NodeSelectorOpDoesNotExist),
		scheduling.NewRequirement(v1beta1.LabelSKUStoragePremiumCapable, corev1.NodeSelectorOpIn, fmt.Sprint(sku.IsPremiumIO())),
		scheduling.NewRequirement(v1beta1.LabelSKUAcceleratedNetworking, corev1.NodeSelectorOpIn, fmt.Sprint(sku.IsAcceleratedNetworkingSupported())),
		scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, corev1.NodeSelectorOpDoesNotExist),
		// all additive feature initialized elsewhere
	)

	// composites
	requirements[v1beta1.LabelSKUName].Insert(sku.GetName())

	// size parts
	requirements[v1beta1.LabelSKUFamily].Insert(vmsize.Family)
	requirements[v1beta1.LabelSKUSeries].Insert(vmsize.Series)

	setRequirementsEphemeralOSDiskSupported(requirements, sku)
	setRequirementsHyperVGeneration(requirements, sku)
	setRequirementsGPU(requirements, sku, vmsize)
	setRequirementsVersion(requirements, vmsize)
	if lo.FromPtr(nodeClass.Spec.FIPSMode) == v1beta1.FIPSModeFIPS {
		requirements[v1beta1.AKSLabelFIPSEnabled].Insert("true")
	}

	return requirements
}

func setRequirementsEphemeralOSDiskSupported(requirements scheduling.Requirements, sku *skewer.SKU) {
	sizeGB, _ := FindMaxEphemeralSizeGBAndPlacement(sku)
	if sizeGB > 0 {
		requirements[v1beta1.LabelSKUStorageEphemeralOSMaxSize].Insert(fmt.Sprint(sizeGB))
	}
}

func setRequirementsHyperVGeneration(requirements scheduling.Requirements, sku *skewer.SKU) {
	if sku.IsHyperVGen1Supported() {
		requirements[v1beta1.LabelSKUHyperVGeneration].Insert(v1beta1.HyperVGenerationV1)
	}
	if sku.IsHyperVGen2Supported() {
		requirements[v1beta1.LabelSKUHyperVGeneration].Insert(v1beta1.HyperVGenerationV2)
	}
}

func setRequirementsGPU(requirements scheduling.Requirements, sku *skewer.SKU, vmsize *skewer.VMSizeType) {
	if utils.IsNvidiaEnabledSKU(sku.GetName()) {
		requirements[v1beta1.LabelSKUGPUManufacturer].Insert(v1beta1.ManufacturerNvidia)
		if vmsize.AcceleratorType != nil {
			requirements[v1beta1.LabelSKUGPUName].Insert(*vmsize.AcceleratorType)
		}
	}
}

// setRequirementsVersion sets the SKU version label, dropping "v" prefix and backfilling "1"
func setRequirementsVersion(requirements scheduling.Requirements, vmsize *skewer.VMSizeType) {
	version := utils.ExtractVersionFromVMSize(vmsize)
	if version == "" {
		return
	}
	requirements[v1beta1.LabelSKUVersion].Insert(version)
}

func getArchitecture(architecture string) string {
	if value, ok := v1beta1.AzureToKubeArchitectures[architecture]; ok {
		return value
	}
	return architecture // unrecognized
}

func computeCapacity(ctx context.Context, sku *skewer.SKU, nodeClass *v1beta1.AKSNodeClass) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:                    *cpu(sku),
		corev1.ResourceMemory:                 *memoryWithoutOverhead(ctx, sku),
		corev1.ResourceEphemeralStorage:       *ephemeralStorage(nodeClass),
		corev1.ResourcePods:                   *pods(ctx, nodeClass),
		corev1.ResourceName("nvidia.com/gpu"): *gpuNvidiaCount(sku),
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

func memoryWithoutOverhead(ctx context.Context, sku *skewer.SKU) *resource.Quantity {
	return CalculateMemoryWithoutOverhead(options.FromContext(ctx).VMMemoryOverheadPercent, memoryGiB(sku))
}

func CalculateMemoryWithoutOverhead(vmMemoryOverheadPercent float64, skuMemoryGiB float64) *resource.Quantity {
	// Consistency in abstractions could be improved here (e.g., units, returning types)
	memory := resources.Quantity(fmt.Sprintf("%dGi", int64(skuMemoryGiB)))
	memory.Sub(*resource.NewQuantity(int64(math.Ceil(
		float64(memory.Value())*vmMemoryOverheadPercent)), resource.DecimalSI))
	return memory
}

func ephemeralStorage(nodeClass *v1beta1.AKSNodeClass) *resource.Quantity {
	return resource.NewScaledQuantity(int64(lo.FromPtr(nodeClass.Spec.OSDiskSizeGB)), resource.Giga)
}

func pods(ctx context.Context, nc *v1beta1.AKSNodeClass) *resource.Quantity {
	networkPlugin, networkPluginMode := options.FromContext(ctx).NetworkPlugin, options.FromContext(ctx).NetworkPluginMode
	return resource.NewQuantity(int64(utils.GetMaxPods(nc, networkPlugin, networkPluginMode)), resource.DecimalSI)
}

func SystemReservedResources() corev1.ResourceList {
	// AKS does not set system-reserved values and only CPU and memory are considered
	// https://learn.microsoft.com/en-us/azure/aks/concepts-clusters-workloads#resource-reservations
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.Quantity{},
		corev1.ResourceMemory: resource.Quantity{},
	}
}

func KubeReservedResources(vcpus int64, memoryGib float64) corev1.ResourceList {
	reservedMemoryMi := int64(1024 * reservedMemoryTaxGi.Calculate(memoryGib))
	reservedCPUMilli := int64(1000 * reservedCPUTaxVCPU.Calculate(float64(vcpus)))

	resources := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewScaledQuantity(reservedCPUMilli, resource.Milli),
		corev1.ResourceMemory: *resource.NewQuantity(reservedMemoryMi*1024*1024, resource.BinarySI),
	}

	return resources
}

func EvictionThreshold() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse(DefaultMemoryAvailable),
	}
}
