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

package fake

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

// test BeginCreateOrUpdate
func TestComputeAPI_BeginCreateOrUpdate(t *testing.T) {
	// setup
	computeAPI := &VirtualMachinesAPI{}
	//computeAPI.VirtualMachineCreateOrUpdateBehavior.Set(func(input *VirtualMachineCreateOrUpdateInput) (*runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse], error) {
	//	return nil, nil
	//})
	// test
	ctx := context.Background()
	vm, errRetriever := instance.CreateVirtualMachine(ctx, computeAPI, "resourceGroupName", "vmName", armcompute.VirtualMachine{})
	// verify
	if errRetriever.GetFrontendErr() != nil {
		t.Errorf("Unexpected frontend error %v", errRetriever.GetFrontendErr())
		return
	}
	asyncErr := errRetriever.WaitForAsyncErr(ctx)
	if asyncErr != nil {
		t.Errorf("Unexpected async error %v", asyncErr)
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
