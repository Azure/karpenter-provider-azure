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
	"testing"

	//nolint:staticcheck // deprecated package used by skewer
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"
	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/karpenter/pkg/utils/resources"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
)

func TestAKSKubeReservedMemoryMiB(t *testing.T) {
	tests := []struct {
		name      string
		maxPods   int32
		memoryMiB int64
		wantMiB   int64
	}{
		{name: "30 pods", maxPods: 30, memoryMiB: 64 * 1024, wantMiB: 650},
		{name: "110 pods", maxPods: 110, memoryMiB: 64 * 1024, wantMiB: 2250},
		{name: "250 pods", maxPods: 250, memoryMiB: 192 * 1024, wantMiB: 5050},
		{name: "4 GiB cap", maxPods: 250, memoryMiB: 4 * 1024, wantMiB: 1024},
		{name: "8 GiB cap", maxPods: 110, memoryMiB: 8 * 1024, wantMiB: 2048},
		{name: "below cap boundary", maxPods: 30, memoryMiB: 2599, wantMiB: 649},
		{name: "at cap boundary", maxPods: 30, memoryMiB: 2600, wantMiB: 650},
		{name: "above cap boundary", maxPods: 30, memoryMiB: 2601, wantMiB: 650},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			NewWithT(t).Expect(aksKubeReservedMemoryMiB(test.maxPods, test.memoryMiB)).To(Equal(test.wantMiB))
		})
	}
}

func TestAKSMemoryReservationProfile(t *testing.T) {
	tests := []struct {
		name                string
		provisionMode       string
		version             string
		enableNodeHardening bool
		want                bool
		wantError           bool
	}{
		{name: "scriptless", provisionMode: consts.ProvisionModeAKSScriptless, version: "1.34.0"},
		{name: "scriptless without version", provisionMode: consts.ProvisionModeAKSScriptless},
		{name: "legacy AKS", provisionMode: consts.ProvisionModeAKSMachineAPI, version: "1.28.15"},
		{name: "AKS boundary", provisionMode: consts.ProvisionModeAKSMachineAPI, version: "1.29.0", want: true},
		{name: "prefixed version", provisionMode: consts.ProvisionModeAKSMachineAPI, version: "v1.34.10", want: true},
		{name: "batched machines", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, version: "1.34.0", want: true},
		{name: "bootstrap client", provisionMode: consts.ProvisionModeBootstrappingClient, version: "1.34.0", want: true},
		{name: "hardened scriptless", provisionMode: consts.ProvisionModeAKSScriptless, version: "1.34.0", enableNodeHardening: true},
		{name: "hardened machines", provisionMode: consts.ProvisionModeAKSMachineAPI, version: "1.34.0", enableNodeHardening: true},
		{name: "hardened batched machines", provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, version: "1.34.0", enableNodeHardening: true},
		{name: "bootstrap client does not apply hardening", provisionMode: consts.ProvisionModeBootstrappingClient, version: "1.34.0", enableNodeHardening: true, want: true},
		{name: "missing version", provisionMode: consts.ProvisionModeAKSMachineAPI, wantError: true},
		{name: "invalid version", provisionMode: consts.ProvisionModeAKSMachineAPI, version: "invalid", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := options.ToContext(context.Background(), &options.Options{
				ProvisionMode:       test.provisionMode,
				EnableNodeHardening: test.enableNodeHardening,
			})
			nodeClass := &v1beta1.AKSNodeClass{}
			nodeClass.Status.KubernetesVersion = lo.ToPtr(test.version)
			nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
			actual, err := usesAKSMemoryReservations(ctx, nodeClass)
			if test.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			g.Expect(actual).To(Equal(test.want))
		})
	}

	t.Run("rejects stale version readiness", func(t *testing.T) {
		g := NewWithT(t)
		ctx := options.ToContext(context.Background(), &options.Options{ProvisionMode: consts.ProvisionModeAKSMachineAPI})
		nodeClass := &v1beta1.AKSNodeClass{}
		nodeClass.Status.KubernetesVersion = lo.ToPtr("1.34.0")
		nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
		nodeClass.Generation++
		actual, err := usesAKSMemoryReservations(ctx, nodeClass)
		g.Expect(err).To(HaveOccurred())
		g.Expect(actual).To(BeFalse())
	})
}

func TestInstanceTypeMemoryReservations(t *testing.T) {
	sku := skewer.SKU(compute.ResourceSku{
		Name: lo.ToPtr("Standard_D48as_v6"),
		Size: lo.ToPtr("D48as_v6"),
		Capabilities: &[]compute.ResourceSkuCapabilities{
			{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("48")},
			{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("192")},
		},
	})
	vmsize := lo.Must(sku.GetVMSize())
	// Four synthetic 10-CPU/42-GiB pods plus 1 CPU/3 GiB of per-node overhead.
	requests := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("41"),
		corev1.ResourceMemory: resource.MustParse("171Gi"),
	}
	tests := []struct {
		name                string
		useAKSReservations  bool
		enableNodeHardening bool
		wantKubeMiB         int64
		wantSystemMiB       int64
		wantEvictionMiB     int64
		wantFits            bool
	}{
		{name: "legacy", wantKubeMiB: 10854, wantEvictionMiB: 750},
		{name: "AKS 1.29+", useAKSReservations: true, wantKubeMiB: 5050, wantEvictionMiB: 100, wantFits: true},
		{name: "hardening takes precedence", useAKSReservations: true, enableNodeHardening: true, wantKubeMiB: 12682, wantSystemMiB: 900, wantEvictionMiB: 512},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := options.ToContext(context.Background(), &options.Options{
				ProvisionMode:           consts.ProvisionModeAKSMachineAPI,
				EnableNodeHardening:     test.enableNodeHardening,
				NetworkPlugin:           consts.NetworkPluginAzure,
				VMMemoryOverheadPercent: 0.075,
			})
			instanceType := newInstanceType(ctx, &sku, vmsize, "westus3", nil, &instanceTypeParameters{
				ImageFamily:              v1beta1.Ubuntu2204ImageFamily,
				OSDiskSizeGB:             128,
				MaxPods:                  250,
				UseAKSMemoryReservations: test.useAKSReservations,
			}, "x64")
			g.Expect(instanceType.Capacity.Memory().Value()).To(Equal(int64(190_696_547_942)))
			g.Expect(instanceType.Overhead.KubeReserved.Cpu().MilliValue()).To(Equal(int64(580)))
			g.Expect(instanceType.Overhead.KubeReserved.Memory().Value()).To(Equal(test.wantKubeMiB * bytesPerMiB))
			g.Expect(instanceType.Overhead.SystemReserved.Memory().Value()).To(Equal(test.wantSystemMiB * bytesPerMiB))
			g.Expect(instanceType.Overhead.EvictionThreshold.Memory().Value()).To(Equal(test.wantEvictionMiB * bytesPerMiB))
			g.Expect(instanceType.Overhead.EvictionThreshold.StorageEphemeral().Value()).To(Equal(int64(12_800_000_190)))
			g.Expect(resources.Fits(requests, instanceType.Allocatable())).To(Equal(test.wantFits))
		})
	}
}

// These cases mirror the hardened kube-reserved memory calculation in the AKS RP.
func TestKubeReservedResourcesHardeningParity(t *testing.T) {
	tests := []struct {
		name          string
		vcpus         int64
		memoryMiB     int64
		maxPods       int32
		wantCPUMilli  int64
		wantMemoryMiB int64
	}{
		{name: "7 GiB with 30 pods", vcpus: 4, memoryMiB: 7 * 1024, maxPods: 30, wantCPUMilli: 140, wantMemoryMiB: 1300},
		{name: "8 GiB with 110 pods is capped", vcpus: 2, memoryMiB: 8 * 1024, maxPods: 110, wantCPUMilli: 100, wantMemoryMiB: 2048},
		{name: "32 GiB with 110 pods", vcpus: 8, memoryMiB: 32 * 1024, maxPods: 110, wantCPUMilli: 180, wantMemoryMiB: 4505},
		{name: "64 GiB with 110 pods", vcpus: 8, memoryMiB: 64 * 1024, maxPods: 110, wantCPUMilli: 180, wantMemoryMiB: 5160},
		{name: "128 GiB with 250 pods", vcpus: 16, memoryMiB: 128 * 1024, maxPods: 250, wantCPUMilli: 260, wantMemoryMiB: 11371},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			resources := KubeReservedResources(test.vcpus, test.memoryMiB, test.maxPods, true)
			cpu := resources[corev1.ResourceCPU]
			memory := resources[corev1.ResourceMemory]
			wantCPU := *resource.NewMilliQuantity(test.wantCPUMilli, resource.DecimalSI)
			wantMemory := *resource.NewQuantity(test.wantMemoryMiB*bytesPerMiB, resource.BinarySI)
			g.Expect(cpu.Cmp(wantCPU)).To(Equal(0))
			g.Expect(memory.Cmp(wantMemory)).To(Equal(0))
		})
	}
}

// These cases mirror the hardened system-reserved calculation in the AKS RP.
func TestSystemReservedResourcesHardeningParity(t *testing.T) {
	tests := []struct {
		name          string
		memoryMiB     int64
		networkPlugin string
		wantMemoryMiB int64
	}{
		{name: "7 GiB without Azure CNI", memoryMiB: 7 * 1024, networkPlugin: consts.NetworkPluginNone, wantMemoryMiB: 200},
		{name: "7 GiB with Azure CNI", memoryMiB: 7 * 1024, networkPlugin: consts.NetworkPluginAzure, wantMemoryMiB: 300},
		{name: "32 GiB without Azure CNI", memoryMiB: 32 * 1024, networkPlugin: consts.NetworkPluginNone, wantMemoryMiB: 300},
		{name: "32 GiB with Azure CNI", memoryMiB: 32 * 1024, networkPlugin: consts.NetworkPluginAzure, wantMemoryMiB: 400},
		{name: "64 GiB without Azure CNI", memoryMiB: 64 * 1024, networkPlugin: consts.NetworkPluginNone, wantMemoryMiB: 400},
		{name: "128 GiB with Azure CNI", memoryMiB: 128 * 1024, networkPlugin: consts.NetworkPluginAzure, wantMemoryMiB: 700},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			resources := SystemReservedResources(test.memoryMiB, test.networkPlugin, true)
			cpu := resources[corev1.ResourceCPU]
			memory := resources[corev1.ResourceMemory]
			ephemeralStorage := resources[corev1.ResourceEphemeralStorage]
			wantCPU := *resource.NewMilliQuantity(systemReservedCPUMillicores, resource.DecimalSI)
			wantMemory := *resource.NewQuantity(test.wantMemoryMiB*bytesPerMiB, resource.BinarySI)

			g.Expect(cpu.Cmp(wantCPU)).To(Equal(0))
			g.Expect(memory.Cmp(wantMemory)).To(Equal(0))
			g.Expect(ephemeralStorage.String()).To(Equal(systemReservedEphemeralStorage))
		})
	}
}

func TestSystemReservedResourcesDisabledPreservesLegacyValues(t *testing.T) {
	g := NewWithT(t)
	resources := SystemReservedResources(64*1024, consts.NetworkPluginAzure, false)
	cpu, hasCPU := resources[corev1.ResourceCPU]
	memory, hasMemory := resources[corev1.ResourceMemory]
	g.Expect(resources).To(HaveLen(2))
	g.Expect(hasCPU).To(BeTrue())
	g.Expect(hasMemory).To(BeTrue())
	g.Expect(cpu.IsZero()).To(BeTrue())
	g.Expect(memory.IsZero()).To(BeTrue())
}

func TestEvictionMemoryLadder(t *testing.T) {
	tests := []struct {
		name        string
		memoryMiB   int64
		wantSoftMiB int64
		wantHardMiB int64
	}{
		{name: "zero memory", memoryMiB: 0, wantSoftMiB: 500, wantHardMiB: 250},
		{name: "exactly 8 GiB", memoryMiB: 8 * 1024, wantSoftMiB: 500, wantHardMiB: 250},
		{name: "just above 8 GiB", memoryMiB: 8*1024 + 1, wantSoftMiB: 750, wantHardMiB: 375},
		{name: "just below 32 GiB", memoryMiB: 32*1024 - 1, wantSoftMiB: 750, wantHardMiB: 375},
		{name: "exactly 32 GiB", memoryMiB: 32 * 1024, wantSoftMiB: 1024, wantHardMiB: 512},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			softMiB, hardMiB := evictionMemoryLadder(test.memoryMiB)
			g.Expect(softMiB).To(Equal(test.wantSoftMiB))
			g.Expect(hardMiB).To(Equal(test.wantHardMiB))
		})
	}
}

func TestEvictionThreshold(t *testing.T) {
	tests := []struct {
		name                string
		memoryMiB           int64
		enableNodeHardening bool
		want                string
	}{
		{name: "small hardened node", memoryMiB: 8 * 1024, enableNodeHardening: true, want: "250Mi"},
		{name: "medium hardened node", memoryMiB: 16 * 1024, enableNodeHardening: true, want: "375Mi"},
		{name: "large hardened node", memoryMiB: 32 * 1024, enableNodeHardening: true, want: "512Mi"},
		{name: "hardening disabled", memoryMiB: 32 * 1024, want: DefaultMemoryAvailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			threshold := EvictionThreshold(test.memoryMiB, resource.MustParse("128G"), test.enableNodeHardening)[corev1.ResourceMemory]
			g.Expect(threshold.String()).To(Equal(test.want))
		})
	}
}

func TestEvictionThresholdEphemeralStorage(t *testing.T) {
	g := NewWithT(t)
	g.Expect(HardEvictionNodeFSAvailable).To(Equal(fmt.Sprintf("%d%%", hardEvictionNodeFSAvailablePercent)))

	tests := []struct {
		name          string
		capacity      string
		expectedBytes int64
	}{
		{name: "128 decimal gigabytes", capacity: "128G", expectedBytes: 12_800_000_190},
		{name: "128 binary gibibytes", capacity: "128Gi", expectedBytes: 13_743_895_552},
		{name: "odd bytes floor", capacity: "101", expectedBytes: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caseG := NewWithT(t)
			threshold := EvictionThreshold(32*1024, resource.MustParse(test.capacity), false)
			storage := threshold[corev1.ResourceEphemeralStorage]
			caseG.Expect(storage.Value()).To(Equal(test.expectedBytes))
		})
	}

	capacity := resource.MustParse("128G")
	eviction := EvictionThreshold(32*1024, capacity, true)[corev1.ResourceEphemeralStorage]
	system := SystemReservedResources(32*1024, consts.NetworkPluginAzure, true)[corev1.ResourceEphemeralStorage]
	g.Expect(eviction.Value()).To(Equal(int64(12_800_000_190)))
	g.Expect(system.String()).To(Equal("1Gi"))
}

func TestSoftEvictionThreshold(t *testing.T) {
	tests := []struct {
		name      string
		memoryMiB int64
		want      string
	}{
		{name: "small node", memoryMiB: 8 * 1024, want: "500Mi"},
		{name: "medium node", memoryMiB: 16 * 1024, want: "750Mi"},
		{name: "large node", memoryMiB: 32 * 1024, want: "1Gi"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			threshold := SoftEvictionThreshold(test.memoryMiB)[corev1.ResourceMemory]
			g.Expect(threshold.String()).To(Equal(test.want))
		})
	}
}
