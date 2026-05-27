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

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// --- Helper builders for assignment tests ---

// mkVM creates a VM with the given SKU and zone for assignment testing.
func mkVM(sku, zone string) *armcompute.VirtualMachine {
	vmSize := armcompute.VirtualMachineSizeTypes(sku)
	vm := &armcompute.VirtualMachine{
		Name: lo.ToPtr("vm-" + sku + "-" + zone),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: &vmSize,
			},
		},
	}
	if zone != "" {
		vm.Zones = []*string{lo.ToPtr(zone)}
	}
	return vm
}

// mkAssignReq creates a VMAssignmentRequest with the given parameters.
func mkAssignReq(name string, skus []string, zones []string) *VMAssignmentRequest {
	itMap := make(map[string]*cloudprovider.InstanceType, len(skus))
	for _, s := range skus {
		itMap[s] = &cloudprovider.InstanceType{Name: s}
	}
	return &VMAssignmentRequest{
		NodeClaimName:   name,
		AcceptableSKUs:  skus,
		AcceptableZones: zones,
		InstanceTypes:   itMap,
	}
}

// --- Tests ---

// TestAssign_ExactMatch verifies that N requests and N matching VMs
// results in all assigned, no surplus, no unmatched.
func TestAssign_ExactMatch(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
		mkAssignReq("nc-2", []string{"Standard_D8s_v3"}, []string{"westus-2"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"),
		mkVM("Standard_D8s_v3", "westus-2"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(2))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(BeEmpty())
	g.Expect(assigned["nc-1"].Zone).To(Equal("westus-1"))
	g.Expect(assigned["nc-2"].Zone).To(Equal("westus-2"))
}

// TestAssign_PartialMatch verifies that when there are more requests than VMs,
// unmatched requests are returned.
func TestAssign_PartialMatch(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
		mkAssignReq("nc-2", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
		mkAssignReq("nc-3", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"),
		mkVM("Standard_D4s_v3", "westus-1"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(2))
	g.Expect(unmatched).To(HaveLen(1))
	g.Expect(unmatched[0].NodeClaimName).To(Equal("nc-3"))
	g.Expect(surplus).To(BeEmpty())
}

// TestAssign_SurplusVMs verifies that when there are more VMs than requests,
// extra VMs are returned as surplus.
func TestAssign_SurplusVMs(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"),
		mkVM("Standard_D4s_v3", "westus-1"),
		mkVM("Standard_D8s_v3", "westus-2"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(1))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(HaveLen(2))
}

// TestAssign_NoOverlap verifies that when no VM matches any request's SKU/zone,
// all requests are unmatched and all VMs are surplus.
func TestAssign_NoOverlap(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D8s_v3", "westus-2"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(BeEmpty())
	g.Expect(unmatched).To(HaveLen(1))
	g.Expect(surplus).To(HaveLen(1))
}

// TestAssign_MultiSKU verifies that a request accepting multiple SKUs matches
// a VM with any one of those SKUs.
func TestAssign_MultiSKU(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3", "Standard_D8s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D8s_v3", "westus-1"), // only the second SKU is available
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(1))
	g.Expect(assigned["nc-1"].InstanceType.Name).To(Equal("Standard_D8s_v3"))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(BeEmpty())
}

// TestAssign_MultiZone verifies that a request accepting multiple zones matches
// a VM in any one of those zones.
func TestAssign_MultiZone(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1", "westus-2"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-2"), // only zone-2 available
	}

	assigned, unmatched, _ := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(1))
	g.Expect(assigned["nc-1"].Zone).To(Equal("westus-2"))
	g.Expect(unmatched).To(BeEmpty())
}

// TestAssign_FIFOOrder verifies that the first request in slice order gets first pick
// when multiple requests have overlapping constraints.
func TestAssign_FIFOOrder(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-first", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
		mkAssignReq("nc-second", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"), // only one VM
	}

	assigned, unmatched, _ := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(1))
	g.Expect(assigned).To(HaveKey("nc-first"))
	g.Expect(unmatched).To(HaveLen(1))
	g.Expect(unmatched[0].NodeClaimName).To(Equal("nc-second"))
}

// TestAssign_CrossProductPrefersEarlierSKU verifies that the iteration order of
// AcceptableSKUs determines preference when multiple buckets have VMs.
func TestAssign_CrossProductPrefersEarlierSKU(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3", "Standard_D8s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"),
		mkVM("Standard_D8s_v3", "westus-1"),
	}

	assigned, _, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned["nc-1"].InstanceType.Name).To(Equal("Standard_D4s_v3"))
	g.Expect(surplus).To(HaveLen(1))
}

// TestAssign_MalformedVMGoesToSurplus verifies that a VM with missing properties
// doesn't crash the matcher and is routed to surplus.
func TestAssign_MalformedVMGoesToSurplus(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		{Properties: nil}, // malformed
		mkVM("Standard_D4s_v3", "westus-1"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(1))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(HaveLen(1)) // the malformed one
}

// TestAssign_NilRequestSkipped verifies that nil entries in the request slice
// are silently skipped without panicking.
func TestAssign_NilRequestSkipped(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		nil,
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
	}
	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, vms, nil)
	g.Expect(assigned).To(HaveLen(1))
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(BeEmpty())
}

// TestAssign_EmptyInputs verifies that nil requests and nil VMs return three nils
// without panicking.
func TestAssign_EmptyInputs(t *testing.T) {
	g := NewWithT(t)

	assigned, unmatched, surplus := AssignVMsToNodeClaims(nil, nil, nil)
	g.Expect(assigned).To(BeNil())
	g.Expect(unmatched).To(BeNil())
	g.Expect(surplus).To(BeNil())
}

// TestAssign_RequestsButNoVMs verifies that when there are requests but no VMs,
// all requests become unmatched.
func TestAssign_RequestsButNoVMs(t *testing.T) {
	g := NewWithT(t)

	requests := []*VMAssignmentRequest{
		mkAssignReq("nc-1", []string{"Standard_D4s_v3"}, []string{"westus-1"}),
		mkAssignReq("nc-2", []string{"Standard_D8s_v3"}, []string{"westus-2"}),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(requests, nil, nil)
	g.Expect(assigned).To(BeEmpty())
	g.Expect(unmatched).To(HaveLen(2))
	g.Expect(surplus).To(BeEmpty())
}

// TestAssign_VMsButNoRequests verifies that when there are VMs but no requests,
// all VMs become surplus.
func TestAssign_VMsButNoRequests(t *testing.T) {
	g := NewWithT(t)

	vms := []*armcompute.VirtualMachine{
		mkVM("Standard_D4s_v3", "westus-1"),
		mkVM("Standard_D8s_v3", "westus-2"),
	}

	assigned, unmatched, surplus := AssignVMsToNodeClaims(nil, vms, nil)
	g.Expect(assigned).To(BeEmpty())
	g.Expect(unmatched).To(BeEmpty())
	g.Expect(surplus).To(HaveLen(2))
}
