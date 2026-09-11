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
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

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
			resources := KubeReservedResources(test.vcpus, test.memoryMiB, test.maxPods, true, nil)
			cpu := resources[corev1.ResourceCPU]
			memory := resources[corev1.ResourceMemory]
			wantCPU := *resource.NewMilliQuantity(test.wantCPUMilli, resource.DecimalSI)
			wantMemory := *resource.NewQuantity(test.wantMemoryMiB*bytesPerMiB, resource.BinarySI)
			g.Expect(cpu.Cmp(wantCPU)).To(Equal(0))
			g.Expect(memory.Cmp(wantMemory)).To(Equal(0))
		})
	}
}

func TestKubeReservedResourcesOverrides(t *testing.T) {
	g := NewWithT(t)
	resources := KubeReservedResources(4, 8192, 110, true, map[string]string{
		"cpu":    "250m",
		"memory": "512Mi",
		"pid":    "2000",
	})

	cpu := resources[corev1.ResourceCPU]
	memory := resources[corev1.ResourceMemory]
	g.Expect(cpu.String()).To(Equal("250m"))
	g.Expect(memory.String()).To(Equal("512Mi"))
	g.Expect(resources).ToNot(HaveKey(corev1.ResourceName("pid")))
}

func TestTypedSchedulingOverrides(t *testing.T) {
	g := NewWithT(t)
	g.Expect(kubeReservedOverrides(&v1beta1.KubeReserved{
		CPUMillicores: lo.ToPtr(int32(250)),
		MemoryMB:      lo.ToPtr(int32(512)),
	})).To(Equal(map[string]string{"cpu": "250m", "memory": "512Mi"}))
	g.Expect(evictionHardOverrides(&v1beta1.EvictionThreshold{
		MemoryAvailable:  lo.ToPtr("333Mi"),
		NodeFsAvailable:  lo.ToPtr("12%"),
		NodeFsInodesFree: lo.ToPtr("7%"),
	})).To(Equal(map[string]string{"memory.available": "333Mi", "nodefs.available": "12%"}))
	g.Expect(kubeReservedOverrides(nil)).To(BeNil())
	g.Expect(kubeReservedOverrides(&v1beta1.KubeReserved{})).To(BeNil())
	g.Expect(evictionHardOverrides(nil)).To(BeNil())
	g.Expect(evictionHardOverrides(&v1beta1.EvictionThreshold{})).To(BeNil())
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
			threshold := EvictionThreshold(test.memoryMiB, resource.MustParse("128G"), test.enableNodeHardening, nil)[corev1.ResourceMemory]
			g.Expect(threshold.String()).To(Equal(test.want))
		})
	}
}

func TestEvictionThresholdOverrides(t *testing.T) {
	g := NewWithT(t)

	absolute := EvictionThreshold(8192, resource.Quantity{}, true, map[string]string{MemoryAvailable: "333Mi"})[corev1.ResourceMemory]
	g.Expect(absolute.String()).To(Equal("333Mi"))

	percentage := EvictionThreshold(8192, resource.Quantity{}, true, map[string]string{MemoryAvailable: "5%"})[corev1.ResourceMemory]
	g.Expect(percentage.Value()).To(Equal(410 * bytesPerMiB))

	for _, value := range []string{"-1%", "101%", "NaN%"} {
		threshold := EvictionThreshold(8192, resource.Quantity{}, true, map[string]string{MemoryAvailable: value})[corev1.ResourceMemory]
		g.Expect(threshold.String()).To(Equal("250Mi"))
	}

	// A nodefs.available override drives the modeled ephemeral-storage overhead
	// so it stays consistent with the kubelet eviction flag on the node.
	capacity := resource.MustParse("100Gi")
	nodefsPercent := EvictionThreshold(8192, capacity, true, map[string]string{NodeFSAvailable: "12%"})[corev1.ResourceEphemeralStorage]
	g.Expect(nodefsPercent.Value()).To(Equal(int64(float64(capacity.Value()) * float64(float32(12)/100))))
	fiveGi := resource.MustParse("5Gi")
	nodefsAbsolute := EvictionThreshold(8192, capacity, true, map[string]string{NodeFSAvailable: "5Gi"})[corev1.ResourceEphemeralStorage]
	g.Expect(nodefsAbsolute.Value()).To(Equal(fiveGi.Value()))
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
			threshold := EvictionThreshold(32*1024, resource.MustParse(test.capacity), false, nil)
			storage := threshold[corev1.ResourceEphemeralStorage]
			caseG.Expect(storage.Value()).To(Equal(test.expectedBytes))
		})
	}

	capacity := resource.MustParse("128G")
	eviction := EvictionThreshold(32*1024, capacity, true, nil)[corev1.ResourceEphemeralStorage]
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
