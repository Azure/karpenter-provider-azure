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

// Package localdns resolves LocalDNS against the VM size a node is actually
// getting.
//
// The NodeClass-wide half of the decision lives on AKSNodeClass itself
// (Spec.LocalDNS.Mode, Status.LocalDNSState) and is a pure read of the object.
// Everything here needs the candidate instance type as well, which is scheduler
// state rather than API state -- hence a provider package rather than
// pkg/apis/v1beta1.
package localdns

import (
	"strconv"

	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// LocalDNS VM size floor. AKS only runs LocalDNS on a node whose VM SKU has
// enough headroom for the proxy; see the "VM SKU capacity" row of the Preferred
// compatibility checks at aka.ms/aks/localdns.
const (
	MinVCPU = 4
	// 256 MB = 244.140625 MiB.
	MinMemoryMiB = 244
)

// SKUMeetsFloor reports whether a VM SKU with the given vCPU count and memory
// (in MiB) clears the LocalDNS floor. This is the single definition of that
// floor: the instance type provider applies it to skewer SKU data when
// filtering for Mode=Required, and InstanceTypeMeetsFloor applies it to an
// already built instance type when resolving Mode=Preferred per node.
func SKUMeetsFloor(vcpu, memoryMiB int64) bool {
	return vcpu >= MinVCPU && memoryMiB >= MinMemoryMiB
}

// InstanceTypeMeetsFloor reports whether the VM size described by reqs can run
// LocalDNS. It reads the SKU's vCPU count and memory off the well-known
// requirements the instance type provider stamps on every instance type, which
// carry the raw skewer values -- the same two numbers the provider's own filter
// reads. Capacity is deliberately not used: Capacity[memory] has
// VMMemoryOverheadPercent subtracted from it, so judging the floor against it
// would make a fixed AKS threshold vary with a Karpenter option.
//
// An instance type missing either requirement is reported as not meeting the
// floor: the provider always sets both, so their absence means we cannot
// establish that the node clears it, and LocalDNS-off is the safe answer.
func InstanceTypeMeetsFloor(reqs scheduling.Requirements) bool {
	value := func(key string) (int64, bool) {
		if !reqs.Has(key) {
			return 0, false
		}
		values := reqs.Get(key).Values()
		if len(values) != 1 {
			return 0, false
		}
		parsed, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	vcpu, ok := value(v1beta1.LabelSKUCPU)
	if !ok {
		return false
	}
	memoryMiB, ok := value(v1beta1.LabelSKUMemory)
	if !ok {
		return false
	}
	return SKUMeetsFloor(vcpu, memoryMiB)
}

// IsSupportedForInstanceType returns whether LocalDNS should be enabled on a
// node of the VM size described by reqs.
//
// Under Required the provider has already filtered out every SKU below the
// floor, so this is equivalent to nodeClass.IsLocalDNSEnabled(). Under
// Preferred it is strictly narrower, and that difference is the point:
// Preferred means "enable LocalDNS where the node can support it", so a
// NodePool that admits both large and small VM sizes gets LocalDNS on the large
// nodes and plain cluster DNS on the small ones, rather than losing the small
// sizes as provisioning candidates.
func IsSupportedForInstanceType(nodeClass *v1beta1.AKSNodeClass, reqs scheduling.Requirements) bool {
	return nodeClass.IsLocalDNSEnabled() && InstanceTypeMeetsFloor(reqs)
}

// ResolveForWire translates Status.LocalDNSState (the source of truth, written
// by Karpenter) into a deterministic Mode to send downstream for a node of the
// VM size described by reqs.
//
// In the aks-rp API contract, LocalDNS state is read-only; only Mode is
// accepted as input. Preferred must therefore never be sent over the wire --
// downstream would otherwise re-interpret it against the single VM size of an
// agent pool, which is not the shape Karpenter provisions in.
//
// Rules:
//   - Mode == Disabled or Required: return Spec as-is.
//   - Mode == Preferred: Required when this NodeClass resolved to Enabled *and*
//     this VM size clears the LocalDNS floor; Disabled otherwise.
func ResolveForWire(nodeClass *v1beta1.AKSNodeClass, reqs scheduling.Requirements) *v1beta1.LocalDNS {
	if nodeClass.Spec.LocalDNS == nil {
		return nil
	}
	if nodeClass.Spec.LocalDNS.Mode != v1beta1.LocalDNSModePreferred {
		return nodeClass.Spec.LocalDNS
	}
	out := nodeClass.Spec.LocalDNS.DeepCopy()
	if IsSupportedForInstanceType(nodeClass, reqs) {
		out.Mode = v1beta1.LocalDNSModeRequired
	} else {
		out.Mode = v1beta1.LocalDNSModeDisabled
	}
	return out
}
