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

import "math"

// Node-hardening reservation formulas.
//
// When node hardening is enabled (a cluster-level AKS feature, surfaced to
// Karpenter via options.Options.NodeHardeningEnabled) the kube-reserved,
// system-reserved and hard-eviction values that Karpenter uses for scheduling
// simulation - and, for self-hosted (scriptless) nodes, renders into the
// kubelet configuration - must match what the AKS RP / NodeProvisioner
// configures on hardened nodes. Otherwise the bin-packing simulation
// (allocatable = capacity - overhead) diverges from reality and nodes are
// over- or under-provisioned.
//
// The AKS RP is the source of truth; keep the formulas below in sync with the
// go.goms.io/aks/rp repository:
//   - kube-reserved memory: resourceprovider/sharedlib/common/kubereserved/utils.go
//     (calculateMemoryReservation, enableNodeHardening = true)
//   - system-reserved:      resourceprovider/sharedlib/common/kubereserved/utils.go
//     (calculateSystemReservedMemoryMiB / buildSystemReserved)
//   - hard-eviction ladder: resourceprovider/server/microsoft.com/containerservice/
//     server/validation/eviction/eviction.go (MemoryLadder)
const (
	// Hardened kube-reserved memory (Linux):
	//   min(35*maxPods + max(250, 2% of totalMemMiB), 25% of totalMemMiB)
	hardenedKubeReservedPerPodMiB int64 = 35
	hardenedKubeReservedFloorMiB  int64 = 250

	// Hardened system-reserved memory (Linux):
	//   200 + 100*floor(memGiB/32) (+100 when Azure CNI)
	systemReservedBaseMiB       int64 = 200
	systemReservedPerStepMiB    int64 = 100
	systemReservedStepGiB       int64 = 32
	systemReservedCNIBonusMiB   int64 = 100
	systemReservedCPUMillicores int64 = 100

	// systemReservedEphemeralStorage mirrors the RP's fixed 1Gi ephemeral-storage
	// system reservation on hardened nodes.
	systemReservedEphemeralStorage = "1Gi"
)

// mibToBytes converts a MiB quantity to bytes.
func mibToBytes(mib int64) int64 {
	return mib * 1024 * 1024
}

// hardenedKubeReservedMemoryMiB returns the hardened kube-reserved memory in MiB:
//
//	min(35*maxPods + max(250, 2% of totalMemoryMiB), 25% of totalMemoryMiB)
//
// Mirrors calculateMemoryReservation(enableNodeHardening=true) in the AKS RP.
func hardenedKubeReservedMemoryMiB(maxPods int32, totalMemoryMiB int64) int64 {
	capMiB := totalMemoryMiB / 4 // 25%
	reservedMiB := hardenedKubeReservedPerPodMiB*int64(maxPods) + max(totalMemoryMiB*2/100, hardenedKubeReservedFloorMiB)
	return min(reservedMiB, capMiB)
}

// systemReservedMemoryMiB returns the hardened system-reserved memory in MiB:
//
//	200 + 100*floor(memGiB/32) (+100 when Azure CNI)
//
// Mirrors calculateSystemReservedMemoryMiB in the AKS RP.
func systemReservedMemoryMiB(memoryGiB float64, isAzureCNI bool) int64 {
	steps := int64(math.Floor(memoryGiB / float64(systemReservedStepGiB)))
	mem := systemReservedBaseMiB + steps*systemReservedPerStepMiB
	if isAzureCNI {
		mem += systemReservedCNIBonusMiB
	}
	return mem
}

// hardEvictionMemoryMiB returns the hardened hard-eviction memory.available
// threshold in MiB, following the VM-size ladder:
//
//	<= 8 GiB:        250
//	> 8 & < 32 GiB:  375
//	>= 32 GiB:       512
//
// Mirrors eviction.MemoryLadder (hard value) in the AKS RP.
func hardEvictionMemoryMiB(totalMemoryMiB int64) int64 {
	switch {
	case totalMemoryMiB >= 32*1024:
		return 512
	case totalMemoryMiB > 8*1024:
		return 375
	default:
		return 250
	}
}
