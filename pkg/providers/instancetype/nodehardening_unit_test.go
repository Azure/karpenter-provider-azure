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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
)

func TestShouldUseNodeHardening(t *testing.T) {
	tests := []struct {
		name          string
		enabled       bool
		provisionMode string
		want          bool
	}{
		{name: "disabled for scriptless", provisionMode: consts.ProvisionModeAKSScriptless},
		{name: "enabled for scriptless", enabled: true, provisionMode: consts.ProvisionModeAKSScriptless, want: true},
		{name: "skipped for bootstrapping client", enabled: true, provisionMode: consts.ProvisionModeBootstrappingClient},
		{name: "enabled for machine API", enabled: true, provisionMode: consts.ProvisionModeAKSMachineAPI, want: true},
		{name: "enabled for machine API header batch", enabled: true, provisionMode: consts.ProvisionModeAKSMachineAPIHeaderBatch, want: true},
		{name: "skipped for unknown mode", enabled: true, provisionMode: "unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldUseNodeHardening(test.enabled, test.provisionMode); got != test.want {
				t.Fatalf("shouldUseNodeHardening(%t, %q) = %t, want %t", test.enabled, test.provisionMode, got, test.want)
			}
		})
	}
}

// These cases mirror calculateMemoryReservation(enableNodeHardening=true) in
// resourceprovider/sharedlib/common/kubereserved/utils.go in the AKS RP.
func TestKubeReservedResourcesHardeningParity(t *testing.T) {
	tests := []struct {
		name          string
		vcpus         int64
		memoryGiB     float64
		maxPods       int32
		wantMemoryMiB int64
	}{
		{name: "7 GiB with 30 pods", vcpus: 4, memoryGiB: 7, maxPods: 30, wantMemoryMiB: 1300},
		{name: "8 GiB with 110 pods is capped", vcpus: 2, memoryGiB: 8, maxPods: 110, wantMemoryMiB: 2048},
		{name: "32 GiB with 110 pods", vcpus: 8, memoryGiB: 32, maxPods: 110, wantMemoryMiB: 4505},
		{name: "64 GiB with 110 pods", vcpus: 8, memoryGiB: 64, maxPods: 110, wantMemoryMiB: 5160},
		{name: "128 GiB with 250 pods", vcpus: 16, memoryGiB: 128, maxPods: 250, wantMemoryMiB: 11371},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := KubeReservedResources(test.vcpus, test.memoryGiB, test.maxPods, true)
			memory := resources[corev1.ResourceMemory]
			wantMemory := *resource.NewQuantity(mibToBytes(test.wantMemoryMiB), resource.BinarySI)
			if memory.Cmp(wantMemory) != 0 {
				t.Fatalf("resources = memory=%s; want memory=%s", memory.String(), wantMemory.String())
			}
		})
	}
}

// These cases mirror calculateSystemReservedMemoryMiB and buildSystemReserved
// in resourceprovider/sharedlib/common/kubereserved/utils.go in the AKS RP.
func TestSystemReservedResourcesHardeningParity(t *testing.T) {
	tests := []struct {
		name          string
		memoryGiB     float64
		isAzureCNI    bool
		wantMemoryMiB int64
	}{
		{name: "7 GiB without Azure CNI", memoryGiB: 7, wantMemoryMiB: 200},
		{name: "7 GiB with Azure CNI", memoryGiB: 7, isAzureCNI: true, wantMemoryMiB: 300},
		{name: "32 GiB without Azure CNI", memoryGiB: 32, wantMemoryMiB: 300},
		{name: "32 GiB with Azure CNI", memoryGiB: 32, isAzureCNI: true, wantMemoryMiB: 400},
		{name: "64 GiB without Azure CNI", memoryGiB: 64, wantMemoryMiB: 400},
		{name: "128 GiB with Azure CNI", memoryGiB: 128, isAzureCNI: true, wantMemoryMiB: 700},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := SystemReservedResources(test.memoryGiB, test.isAzureCNI, true)
			cpu := resources[corev1.ResourceCPU]
			memory := resources[corev1.ResourceMemory]
			ephemeralStorage := resources[corev1.ResourceEphemeralStorage]
			wantCPU := *resource.NewMilliQuantity(systemReservedCPUMillicores, resource.DecimalSI)
			wantMemory := *resource.NewQuantity(mibToBytes(test.wantMemoryMiB), resource.BinarySI)

			if cpu.Cmp(wantCPU) != 0 || memory.Cmp(wantMemory) != 0 || ephemeralStorage.String() != systemReservedEphemeralStorage {
				t.Fatalf("resources = cpu=%s,memory=%s,ephemeral-storage=%s; want cpu=%s,memory=%s,ephemeral-storage=%s",
					cpu.String(), memory.String(), ephemeralStorage.String(), wantCPU.String(), wantMemory.String(), systemReservedEphemeralStorage)
			}
		})
	}
}

func TestSystemReservedResourcesDisabledPreservesLegacyValues(t *testing.T) {
	resources := SystemReservedResources(64, true, false)
	cpu, hasCPU := resources[corev1.ResourceCPU]
	memory, hasMemory := resources[corev1.ResourceMemory]
	if len(resources) != 2 || !hasCPU || !hasMemory || !cpu.IsZero() || !memory.IsZero() {
		t.Fatalf("resources = %#v, want exactly zero-valued CPU and memory", resources)
	}
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
			softMiB, hardMiB := evictionMemoryLadder(test.memoryMiB)
			if softMiB != test.wantSoftMiB || hardMiB != test.wantHardMiB {
				t.Fatalf("evictionMemoryLadder(%d) = (%d, %d), want (%d, %d)", test.memoryMiB, softMiB, hardMiB, test.wantSoftMiB, test.wantHardMiB)
			}
		})
	}
}
