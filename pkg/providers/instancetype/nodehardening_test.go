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

package instancetype_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

// These tests lock Karpenter's node-hardening scheduling-simulation outputs against the
// reference values produced by the AKS RP for the same inputs. If either side changes,
// the two must be updated in lockstep.
//
// RP references:
//   - aks-rp/resourceprovider/sharedlib/common/kubereserved/utils.go
//     (ReserveCPUAndMemory, ReserveSystemForMemoryGB)
//   - aks-rp/resourceprovider/server/microsoft.com/containerservice/server/validation/
//     eviction/eviction.go (MemoryLadder)

func mustParseMi(mi int64) resource.Quantity {
	return *resource.NewQuantity(mi*1024*1024, resource.BinarySI)
}

func TestKubeReservedResources_HardeningParity(t *testing.T) {
	// Formula (hardening on): min(35*maxPods + max(250, 2%*totalMiB), 25%*totalMiB) MiB.
	cases := []struct {
		name      string
		vcpus     int64
		memoryGiB float64
		maxPods   int64
		expectMiB int64
	}{
		// 7 GiB (7168 MiB): 2% = 143 → floor 250. 35*30 + 250 = 1300. Cap 25% = 1792. → 1300.
		{"7GiB_30pods", 4, 7.0, 30, 1300},
		// 8 GiB (8192 MiB): 2% = 163 → floor 250. 35*110 + 250 = 4100. Cap 2048. → 2048.
		{"8GiB_110pods_capped", 2, 8.0, 110, 2048},
		// 32 GiB (32768 MiB): 2% = 655. 35*110 + 655 = 4505. Cap 8192. → 4505.
		{"32GiB_110pods", 8, 32.0, 110, 4505},
		// 64 GiB (65536 MiB): 2% = 1310. 35*110 + 1310 = 5160. Cap 16384. → 5160.
		{"64GiB_110pods", 8, 64.0, 110, 5160},
		// 128 GiB (131072 MiB): 2% = 2621. 35*250 + 2621 = 11371. Cap 32768. → 11371.
		{"128GiB_250pods", 16, 128.0, 250, 11371},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := instancetype.KubeReservedResources(tc.vcpus, tc.memoryGiB, tc.maxPods, true)
			gotMem := got[corev1.ResourceMemory]
			want := mustParseMi(tc.expectMiB)
			if gotMem.Cmp(want) != 0 {
				t.Fatalf("memory: got %s, want %s", gotMem.String(), want.String())
			}
		})
	}
}

func TestSystemReservedResources_HardeningParity(t *testing.T) {
	// Formula (hardening on): cpu=100m, mem = 200 + 100*floor(GiB/32) [+100 if Azure CNI] MiB.
	cases := []struct {
		name        string
		memoryGiB   float64
		isAzureCNI  bool
		expectCPUmi int64
		expectMiB   int64
	}{
		{"7GiB_no_cni", 7.0, false, 100, 200},
		{"7GiB_azure_cni", 7.0, true, 100, 300},
		{"32GiB_no_cni", 32.0, false, 100, 300},
		{"32GiB_azure_cni", 32.0, true, 100, 400},
		{"64GiB_no_cni", 64.0, false, 100, 400},
		{"128GiB_azure_cni", 128.0, true, 100, 700},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := instancetype.SystemReservedResources(tc.memoryGiB, true, tc.isAzureCNI)
			gotCPU := got[corev1.ResourceCPU]
			wantCPU := *resource.NewScaledQuantity(tc.expectCPUmi, resource.Milli)
			if gotCPU.Cmp(wantCPU) != 0 {
				t.Fatalf("cpu: got %s, want %s", gotCPU.String(), wantCPU.String())
			}
			gotMem := got[corev1.ResourceMemory]
			wantMem := mustParseMi(tc.expectMiB)
			if gotMem.Cmp(wantMem) != 0 {
				t.Fatalf("memory: got %s, want %s", gotMem.String(), wantMem.String())
			}
		})
	}
}

func TestSystemReservedResources_HardeningOffKeepsLegacyStub(t *testing.T) {
	got := instancetype.SystemReservedResources(64.0, false, true)
	cpu := got[corev1.ResourceCPU]
	mem := got[corev1.ResourceMemory]
	if !cpu.IsZero() || !mem.IsZero() {
		t.Fatalf("expected zero-valued CPU and memory when hardening off, got cpu=%s mem=%s",
			cpu.String(), mem.String())
	}
}

func TestEvictionThreshold_HardeningParity(t *testing.T) {
	// MemoryLadder (hardening on): ≤8 GiB→250, >8..<32→375, ≥32→512 MiB.
	cases := []struct {
		name      string
		memoryGiB float64
		expectMiB int64
	}{
		{"4GiB", 4.0, 250},
		{"8GiB_lower_tier", 8.0, 250},
		{"16GiB", 16.0, 375},
		{"32GiB_upper_tier", 32.0, 512},
		{"128GiB", 128.0, 512},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := instancetype.EvictionThreshold(tc.memoryGiB, true)
			gotMem := got[corev1.ResourceMemory]
			want := mustParseMi(tc.expectMiB)
			if gotMem.Cmp(want) != 0 {
				t.Fatalf("memory: got %s, want %s", gotMem.String(), want.String())
			}
		})
	}
}

func TestEvictionThreshold_HardeningOffKeepsDefault(t *testing.T) {
	got := instancetype.EvictionThreshold(128.0, false)
	want := resource.MustParse(instancetype.DefaultMemoryAvailable)
	gotMem := got[corev1.ResourceMemory]
	if gotMem.Cmp(want) != 0 {
		t.Fatalf("memory: got %s, want %s", gotMem.String(), want.String())
	}
}
