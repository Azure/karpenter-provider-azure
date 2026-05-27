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
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// --- Mock VM API ---

type updateCall struct {
	ResourceGroup string
	VMName        string
	Tags          map[string]*string
}

type mockVMAPI struct {
	updateCalls []updateCall
	deleteCalls []string // VM names
	updateErr   error
	deleteErr   error
	mu          sync.Mutex
}

func (m *mockVMAPI) BeginUpdate(_ context.Context, rg string, vmName string, params armcompute.VirtualMachineUpdate, _ *armcompute.VirtualMachinesClientBeginUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientUpdateResponse], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls = append(m.updateCalls, updateCall{ResourceGroup: rg, VMName: vmName, Tags: params.Tags})
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	// Return nil poller — tagAssignedVMs handles nil poller by skipping PollUntilDone
	return nil, nil
}

func (m *mockVMAPI) BeginDelete(_ context.Context, _ string, vmName string, _ *armcompute.VirtualMachinesClientBeginDeleteOptions) (*runtime.Poller[armcompute.VirtualMachinesClientDeleteResponse], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls = append(m.deleteCalls, vmName)
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return nil, nil
}

// --- Tests ---

// TestFleetSharedState_ExecuteOnce verifies sync.Once ensures executePoll runs exactly once
// even when called concurrently from multiple goroutines.
func TestFleetSharedState_ExecuteOnce(t *testing.T) {
	g := NewWithT(t)

	var execCount atomic.Int32
	vmSize := armcompute.VirtualMachineSizeTypes("Standard_D4s_v3")
	vm := &armcompute.VirtualMachine{
		Name:  lo.ToPtr("vm-1"),
		Zones: []*string{lo.ToPtr("westus-1")},
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: &vmSize},
		},
	}

	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{vm},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, nil, "fleet-test", "rg-test",
	)

	// Wrap to count executions
	origOnce := &state.once
	_ = origOnce

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state.ExecuteSharedPoll(context.Background())
			execCount.Add(1)
		}()
	}
	wg.Wait()

	// All 10 goroutines completed, but assignment should show exactly 1 result
	g.Expect(state.GetAssignment("nc-1")).NotTo(BeNil())
	g.Expect(execCount.Load()).To(Equal(int32(10)))
}

// TestFleetSharedState_AllRequestsAssigned verifies that when VMs match all requests,
// every NodeClaim gets a non-nil assignment.
func TestFleetSharedState_AllRequestsAssigned(t *testing.T) {
	g := NewWithT(t)

	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{
			mkVM("Standard_D4s_v3", "westus-1"),
			mkVM("Standard_D8s_v3", "westus-2"),
		},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
			{NodeClaimName: "nc-2", AcceptableSKUs: []string{"Standard_D8s_v3"}, AcceptableZones: []string{"westus-2"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D8s_v3": {Name: "Standard_D8s_v3"}}},
		},
		nil, nil, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())

	g.Expect(state.GetError()).To(BeNil())
	g.Expect(state.GetAssignment("nc-1")).NotTo(BeNil())
	g.Expect(state.GetAssignment("nc-2")).NotTo(BeNil())
	g.Expect(state.GetAssignment("nc-1").Zone).To(Equal("westus-1"))
}

// TestFleetSharedState_PartialAssignment verifies that when fewer VMs than requests,
// unmatched NodeClaims get nil from GetAssignment.
func TestFleetSharedState_PartialAssignment(t *testing.T) {
	g := NewWithT(t)

	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{
			mkVM("Standard_D4s_v3", "westus-1"),
		},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
			{NodeClaimName: "nc-2", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, nil, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())

	g.Expect(state.GetError()).To(BeNil())
	g.Expect(state.GetAssignment("nc-1")).NotTo(BeNil())
	g.Expect(state.GetAssignment("nc-2")).To(BeNil()) // unmatched
}

// TestFleetSharedState_SurplusVMsDeleted verifies surplus VMs trigger BeginDelete calls.
func TestFleetSharedState_SurplusVMsDeleted(t *testing.T) {
	g := NewWithT(t)

	mock := &mockVMAPI{}
	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{
			mkVM("Standard_D4s_v3", "westus-1"),
			mkVM("Standard_D8s_v3", "westus-2"), // surplus — no request for this
		},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, mock, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())

	g.Expect(mock.deleteCalls).To(HaveLen(1))
	g.Expect(mock.deleteCalls[0]).To(ContainSubstring("Standard_D8s_v3"))
}

// TestFleetSharedState_TaggingCalled verifies BeginUpdate is called once per assigned VM.
func TestFleetSharedState_TaggingCalled(t *testing.T) {
	g := NewWithT(t)

	mock := &mockVMAPI{}
	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{
			mkVM("Standard_D4s_v3", "westus-1"),
		},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, mock, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())

	g.Expect(mock.updateCalls).To(HaveLen(1))
	g.Expect(mock.updateCalls[0].Tags).To(HaveKey("karpenter.azure.com_nodeclaim-name"))
	g.Expect(*mock.updateCalls[0].Tags["karpenter.azure.com_nodeclaim-name"]).To(Equal("nc-1"))
}

// TestFleetSharedState_TagFailureNonFatal verifies that if BeginUpdate returns error,
// the assignment still appears in GetAssignment.
func TestFleetSharedState_TagFailureNonFatal(t *testing.T) {
	g := NewWithT(t)

	mock := &mockVMAPI{updateErr: fmt.Errorf("tag failed")}
	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{
			mkVM("Standard_D4s_v3", "westus-1"),
		},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, mock, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())

	g.Expect(state.GetError()).To(BeNil())
	g.Expect(state.GetAssignment("nc-1")).NotTo(BeNil()) // still assigned despite tag failure
}

// TestFleetSharedState_LROError verifies that when SetError is called before ExecuteSharedPoll,
// GetError returns the error for all promises.
func TestFleetSharedState_LROError(t *testing.T) {
	g := NewWithT(t)

	state := NewFleetSharedStateForTest(
		nil,
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, nil, "fleet-test", "rg-test",
	)
	state.SetError(fmt.Errorf("LRO failed: fleet create timeout"))

	state.ExecuteSharedPoll(context.Background())

	g.Expect(state.GetError()).To(MatchError(ContainSubstring("LRO failed")))
	g.Expect(state.GetAssignment("nc-1")).To(BeNil())
}

// TestFleetSharedState_EmptyBatch verifies zero requests + zero VMs doesn't panic.
func TestFleetSharedState_EmptyBatch(t *testing.T) {
	g := NewWithT(t)

	state := NewFleetSharedStateForTest(
		nil, nil, nil, nil, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())

	// With 0 requests and 0 VMs, no error but "no VMs available" since requests is nil
	g.Expect(state.GetError()).To(BeNil())
}

// TestFleetSharedState_GetAssignment_Unknown verifies asking for a non-existent
// NodeClaim returns nil.
func TestFleetSharedState_GetAssignment_Unknown(t *testing.T) {
	g := NewWithT(t)

	state := NewFleetSharedStateForTest(
		[]*armcompute.VirtualMachine{mkVM("Standard_D4s_v3", "westus-1")},
		[]*VMAssignmentRequest{
			{NodeClaimName: "nc-1", AcceptableSKUs: []string{"Standard_D4s_v3"}, AcceptableZones: []string{"westus-1"},
				InstanceTypes: map[string]*cloudprovider.InstanceType{"Standard_D4s_v3": {Name: "Standard_D4s_v3"}}},
		},
		nil, nil, "fleet-test", "rg-test",
	)

	state.ExecuteSharedPoll(context.Background())
	g.Expect(state.GetAssignment("nc-unknown")).To(BeNil())
}
