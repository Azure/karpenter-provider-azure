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
	"time"
)

// Node-hardening reservation formulas.
//
// When node hardening is enabled (a cluster-level AKS feature, surfaced to
// Karpenter via options.Options.EnableNodeHardening) the kube-reserved,
// system-reserved and eviction values that Karpenter uses for scheduling
// simulation and kubelet configuration must match what AKS configures on the
// node. Otherwise the bin-packing simulation (allocatable = capacity -
// overhead) diverges from reality and nodes are over- or under-provisioned.
//
// The AKS RP is the source of truth; keep the formulas below in sync with its
// node-hardening behavior.
const (
	// Hardened system-reserved memory (Linux):
	//   200 + 100*floor(memGiB/32) (+100 when Azure CNI)
	systemReservedBaseMiB       int64 = 200
	systemReservedPerStepMiB    int64 = 100
	systemReservedStepGiB       int64 = 32
	systemReservedCNIBonusMiB   int64 = 100
	systemReservedCPUMillicores int64 = 100
	bytesPerMiB                 int64 = 1024 * 1024

	// systemReservedEphemeralStorage mirrors the RP's fixed 1Gi ephemeral-storage
	// system reservation on hardened nodes.
	systemReservedEphemeralStorage = "1Gi"

	NodeFSAvailable                      = "nodefs.available"
	NodeFSInodesFree                     = "nodefs.inodesFree"
	PIDAvailable                         = "pid.available"
	HardEvictionNodeFSAvailable          = "10%"
	HardEvictionNodeFSInodesFree         = "5%"
	HardEvictionPIDAvailable             = "2000"
	SoftEvictionNodeFSAvailable          = "12%"
	SoftEvictionNodeFSInodesFree         = "7%"
	SoftEvictionMemoryGracePeriod        = 30 * time.Second
	SoftEvictionNodeFSGracePeriod        = 2 * time.Minute
	SoftEvictionNodeFSInodesGracePeriod  = 2 * time.Minute
	SoftEvictionMaxPodGracePeriodSeconds = int32(60)
	KubeReservedPIDs                     = "1000"
	SystemReservedPIDs                   = "1000"
)

// hardenedKubeReservedMemoryMiB returns the hardened kube-reserved memory in MiB:
//
//	min(35*maxPods + max(250, 2% of totalMemoryMiB), 25% of totalMemoryMiB)
//
// Mirrors the hardened kube-reserved memory calculation in the AKS RP.
func hardenedKubeReservedMemoryMiB(maxPods int32, totalMemoryMiB int64) int64 {
	capMiB := totalMemoryMiB / 4 // 25%
	reservedMiB := 35*int64(maxPods) + max(totalMemoryMiB*2/100, 250)
	return min(reservedMiB, capMiB)
}

// systemReservedMemoryMiB returns the hardened system-reserved memory in MiB:
//
//	200 + 100*floor(totalMemoryMiB/(32*1024)) (+100 when Azure CNI)
//
// Mirrors the hardened system-reserved memory calculation in the AKS RP.
func systemReservedMemoryMiB(totalMemoryMiB int64, isAzureCNI bool) int64 {
	steps := totalMemoryMiB / (systemReservedStepGiB * 1024)
	mem := systemReservedBaseMiB + steps*systemReservedPerStepMiB
	if isAzureCNI {
		mem += systemReservedCNIBonusMiB
	}
	return mem
}

// evictionMemoryLadder returns the hardened soft- and hard-eviction
// memory.available thresholds in MiB, following the VM-size ladder:
//
//	<= 8 GiB:        soft=500,  hard=250
//	> 8 & < 32 GiB:  soft=750,  hard=375
//	>= 32 GiB:       soft=1024, hard=512
//
// Mirrors the hardened eviction memory ladder in the AKS RP.
func evictionMemoryLadder(totalMemoryMiB int64) (softMiB, hardMiB int64) {
	switch {
	case totalMemoryMiB >= 32*1024:
		return 1024, 512
	case totalMemoryMiB > 8*1024:
		return 750, 375
	default:
		return 500, 250
	}
}
