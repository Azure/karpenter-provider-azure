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

package fleet

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

// FleetAssignment is the Fleet-created VM assigned to one NodeClaim.
type FleetAssignment struct {
	VM *armcompute.VirtualMachine
	// SKU is the VM size Fleet actually selected.
	SKU string
	// Zone is in AKS label format (for example "westus2-1", or zones.Regional for regional VMs).
	Zone string
}

// AssignVMsToNodeClaims pairs NodeClaims with Fleet-created VMs in FIFO order.
// Every request in a batch shares the same Fleet body, so any VM Fleet created satisfies
// any request; the assignment records whatever SKU and zone Fleet chose.
//
// Returns the assignments by NodeClaim name, NodeClaims left without a VM, and surplus VMs
// (extra VMs or VMs missing size/zone information).
func AssignVMsToNodeClaims(
	nodeClaimNames []string,
	vms []*armcompute.VirtualMachine,
) (assigned map[string]*FleetAssignment, unmatched []string, surplus []*armcompute.VirtualMachine) {
	assignable := make([]*armcompute.VirtualMachine, 0, len(vms))
	for _, vm := range vms {
		if _, _, ok := skuAndZone(vm); !ok {
			surplus = append(surplus, vm)
			continue
		}
		assignable = append(assignable, vm)
	}

	assigned = make(map[string]*FleetAssignment, len(nodeClaimNames))
	next := 0
	for _, name := range nodeClaimNames {
		if next >= len(assignable) {
			unmatched = append(unmatched, name)
			continue
		}
		vm := assignable[next]
		next++
		sku, zone, _ := skuAndZone(vm)
		assigned[name] = &FleetAssignment{VM: vm, SKU: sku, Zone: zone}
	}
	surplus = append(surplus, assignable[next:]...)
	return assigned, unmatched, surplus
}

// skuAndZone extracts the VM size and AKS-label zone, returning ok=false for malformed VMs.
func skuAndZone(vm *armcompute.VirtualMachine) (string, string, bool) {
	if vm == nil || vm.Properties == nil ||
		vm.Properties.HardwareProfile == nil ||
		vm.Properties.HardwareProfile.VMSize == nil {
		return "", "", false
	}
	zone, err := zones.MakeAKSLabelZoneFromVM(vm)
	if err != nil {
		return "", "", false
	}
	return string(*vm.Properties.HardwareProfile.VMSize), zone, true
}
