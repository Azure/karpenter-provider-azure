// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fake

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter/pkg/providers/instance"
)

// test BeginCreateOrUpdate
func TestComputeAPI_BeginCreateOrUpdate(t *testing.T) {
	// setup
	computeAPI := &VirtualMachinesAPI{}
	//computeAPI.VirtualMachineCreateOrUpdateBehavior.Set(func(input *VirtualMachineCreateOrUpdateInput) (*runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse], error) {
	//	return nil, nil
	//})
	// test
	vm, err := instance.CreateVirtualMachine(context.Background(), computeAPI, "resourceGroupName", "vmName", armcompute.VirtualMachine{})
	// verify
	if err != nil {
		t.Errorf("Unexpected error %v", err)
		return
	}
	if vm == nil {
		t.Errorf("Unexpected nil vm")
		return
	}
	if vm.ID == nil || *(vm.ID) != "/subscriptions/subscriptionID/resourceGroups/resourceGroupName/providers/Microsoft.Compute/virtualMachines/vmName" {
		t.Errorf("Unexpected vm.ID %v", vm.ID)
	}
}
