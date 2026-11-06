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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

func computeVM(name, size, zone string) *armcompute.VirtualMachine {
	vm := &armcompute.VirtualMachine{
		Name:     lo.ToPtr(name),
		Location: lo.ToPtr("eastus"),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypes(size))},
		},
	}
	if zone != "" {
		vm.Zones = []*string{lo.ToPtr(zone)}
	}
	return vm
}

func TestAssignVMsToNodeClaims_FIFO(t *testing.T) {
	g := NewWithT(t)
	vmA := computeVM("vm-a", "Standard_D4s_v3", "1")
	vmB := computeVM("vm-b", "Standard_D8s_v3", "")

	assigned, unmatched, surplus := AssignVMsToNodeClaims([]string{"claim-a", "claim-b"}, []*armcompute.VirtualMachine{vmA, vmB})

	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(BeEmpty())
	g.Expect(assigned).To(HaveLen(2))
	g.Expect(assigned["claim-a"]).To(Equal(&FleetAssignment{VM: vmA, SKU: "Standard_D4s_v3", Zone: "eastus-1"}))
	g.Expect(assigned["claim-b"]).To(Equal(&FleetAssignment{VM: vmB, SKU: "Standard_D8s_v3", Zone: zones.Regional}))
}

func TestAssignVMsToNodeClaims_FewerVMsThanNodeClaims(t *testing.T) {
	g := NewWithT(t)
	vmA := computeVM("vm-a", "Standard_D4s_v3", "1")

	assigned, unmatched, surplus := AssignVMsToNodeClaims([]string{"claim-a", "claim-b", "claim-c"}, []*armcompute.VirtualMachine{vmA})

	g.Expect(assigned).To(HaveLen(1))
	g.Expect(assigned["claim-a"].VM).To(BeIdenticalTo(vmA))
	g.Expect(unmatched).To(Equal([]string{"claim-b", "claim-c"}))
	g.Expect(surplus).To(BeEmpty())
}

func TestAssignVMsToNodeClaims_MoreVMsThanNodeClaims(t *testing.T) {
	g := NewWithT(t)
	vmA := computeVM("vm-a", "Standard_D4s_v3", "1")
	vmB := computeVM("vm-b", "Standard_D4s_v3", "2")

	assigned, unmatched, surplus := AssignVMsToNodeClaims([]string{"claim-a"}, []*armcompute.VirtualMachine{vmA, vmB})

	g.Expect(assigned).To(HaveLen(1))
	g.Expect(assigned["claim-a"].VM).To(BeIdenticalTo(vmA))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(ConsistOf(BeIdenticalTo(vmB)))
}

func TestAssignVMsToNodeClaims_MalformedVMsAreSurplus(t *testing.T) {
	g := NewWithT(t)
	noSize := &armcompute.VirtualMachine{Name: lo.ToPtr("no-size")}
	multiZone := computeVM("multi-zone", "Standard_D4s_v3", "1")
	multiZone.Zones = append(multiZone.Zones, lo.ToPtr("2"))
	noLocation := computeVM("no-location", "Standard_D4s_v3", "1")
	noLocation.Location = nil
	valid := computeVM("valid", "Standard_D4s_v3", "3")

	assigned, unmatched, surplus := AssignVMsToNodeClaims(
		[]string{"claim-a"},
		[]*armcompute.VirtualMachine{nil, noSize, multiZone, noLocation, valid},
	)

	g.Expect(assigned).To(HaveLen(1))
	g.Expect(assigned["claim-a"].VM).To(BeIdenticalTo(valid))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(HaveLen(4))
}

func TestAssignVMsToNodeClaims_Empty(t *testing.T) {
	g := NewWithT(t)

	assigned, unmatched, surplus := AssignVMsToNodeClaims(nil, nil)

	g.Expect(assigned).To(BeEmpty())
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(BeEmpty())
}

func TestNewFleetSharedState_NoVMs(t *testing.T) {
	g := NewWithT(t)

	state := newFleetSharedState("fleet-a", []string{"claim-a"}, nil)

	g.Expect(state.GetError()).To(MatchError("fleet fleet-a created no VMs"))
	g.Expect(state.GetAssignment("claim-a")).To(BeNil())
	g.Expect(state.FleetName()).To(Equal("fleet-a"))
}

func TestNewFleetSharedState_RecordsAssignmentsAndSurplus(t *testing.T) {
	g := NewWithT(t)
	vmA := computeVM("vm-a", "Standard_D4s_v3", "1")
	vmB := computeVM("vm-b", "Standard_D4s_v3", "2")

	state := newFleetSharedState("fleet-a", []string{"claim-a"}, []*armcompute.VirtualMachine{vmA, vmB})

	g.Expect(state.GetError()).NotTo(HaveOccurred())
	g.Expect(state.GetAssignment("claim-a").VM).To(BeIdenticalTo(vmA))
	g.Expect(state.GetAssignment("unknown")).To(BeNil())
	g.Expect(state.Surplus()).To(ConsistOf(BeIdenticalTo(vmB)))
}
