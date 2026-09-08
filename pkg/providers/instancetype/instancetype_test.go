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
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

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
			threshold := EvictionThreshold(test.memoryMiB, test.enableNodeHardening)[corev1.ResourceMemory]
			g.Expect(threshold.String()).To(Equal(test.want))
		})
	}
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
