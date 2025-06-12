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
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
)

type VirtualMachineCreateOrUpdateInput struct {
	ResourceGroupName string
	VMName            string
	VM                armcompute.VirtualMachine
	Options           *armcompute.VirtualMachinesClientBeginCreateOrUpdateOptions
}

type VirtualMachineUpdateInput struct {
	ResourceGroupName string
	VMName            string
	Updates           armcompute.VirtualMachineUpdate
	Options           *armcompute.VirtualMachinesClientBeginUpdateOptions
}

type VirtualMachineDeleteInput struct {
	ResourceGroupName string
	VMName            string
	Options           *armcompute.VirtualMachinesClientBeginDeleteOptions
}

type VirtualMachineGetInput struct {
	ResourceGroupName string
	VMName            string
	Options           *armcompute.VirtualMachinesClientGetOptions
}

type VirtualMachinesBehavior struct {
	VirtualMachineCreateOrUpdateBehavior MockedLRO[VirtualMachineCreateOrUpdateInput, armcompute.VirtualMachinesClientCreateOrUpdateResponse]
	VirtualMachineUpdateBehavior         MockedLRO[VirtualMachineUpdateInput, armcompute.VirtualMachinesClientUpdateResponse]
	VirtualMachineDeleteBehavior         MockedLRO[VirtualMachineDeleteInput, armcompute.VirtualMachinesClientDeleteResponse]
	VirtualMachineGetBehavior            MockedFunction[VirtualMachineGetInput, armcompute.VirtualMachinesClientGetResponse]
	Instances                            sync.Map
}

// assert that the fake implements the interface
var _ instance.VirtualMachinesAPI = &VirtualMachinesAPI{}

type VirtualMachinesAPI struct {
	// TODO: document the implications of embedding vs. not embedding the interface here
	// instance.VirtualMachinesAPI // - this is the interface we are mocking.
	VirtualMachinesBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *VirtualMachinesAPI) Reset() {
	c.VirtualMachineCreateOrUpdateBehavior.Reset()
	c.VirtualMachineDeleteBehavior.Reset()
	c.VirtualMachineGetBehavior.Reset()
	c.VirtualMachineUpdateBehavior.Reset()
	c.Instances.Range(func(k, v any) bool {
		c.Instances.Delete(k)
		return true
	})
}

func (c *VirtualMachinesAPI) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, vmName string, parameters armcompute.VirtualMachine, options *armcompute.VirtualMachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientCreateOrUpdateResponse], error) {
	// gather input parameters (may get rid of this with multiple mocked function signatures to reflect common patterns)
	input := &VirtualMachineCreateOrUpdateInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		VM:                parameters,
		Options:           options,
	}

	return c.VirtualMachineCreateOrUpdateBehavior.Invoke(input, func(input *VirtualMachineCreateOrUpdateInput) (*armcompute.VirtualMachinesClientCreateOrUpdateResponse, error) {
		// example of input validation
		//if input.ResourceGroupName == "" {
		//	return nil, errors.New("ResourceGroupName is required")
		//}
		// TODO: may have to clone ...
		// TODO: subscription ID?
		vm := input.VM
		id := utils.MkVMID(input.ResourceGroupName, input.VMName)
		vm.ID = to.StringPtr(id)
		vm.Name = to.StringPtr(input.VMName)
		if vm.Properties == nil {
			vm.Properties = &armcompute.VirtualMachineProperties{}
		}
		if vm.Properties.TimeCreated == nil {
			vm.Properties.TimeCreated = lo.ToPtr(time.Now()) // TODO: use simulated time?
		}
		c.Instances.Store(id, vm)
		return &armcompute.VirtualMachinesClientCreateOrUpdateResponse{
			VirtualMachine: vm,
		}, nil
	})
}

func (c *VirtualMachinesAPI) BeginUpdate(_ context.Context, resourceGroupName string, vmName string, updates armcompute.VirtualMachineUpdate, options *armcompute.VirtualMachinesClientBeginUpdateOptions) (*runtime.Poller[armcompute.VirtualMachinesClientUpdateResponse], error) {
	input := &VirtualMachineUpdateInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		Updates:           updates,
		Options:           options,
	}
	return c.VirtualMachineUpdateBehavior.Invoke(input, func(input *VirtualMachineUpdateInput) (*armcompute.VirtualMachinesClientUpdateResponse, error) {
		id := utils.MkVMID(input.ResourceGroupName, input.VMName)

		instance, ok := c.Instances.Load(id)
		if !ok {
			return nil, &azcore.ResponseError{ErrorCode: errors.ResourceNotFound}
		}
		vm := instance.(armcompute.VirtualMachine)

		// If other fields need to be updated in the future, you can similarly
		// update the VM object by merging with updates.<New Field>.
		vm.Tags = updates.Tags
		if updates.Identity != nil {
			if vm.Identity == nil {
				vm.Identity = &armcompute.VirtualMachineIdentity{}
			}

			if updates.Identity.Type != nil {
				vm.Identity.Type = updates.Identity.Type
			}
			if len(updates.Identity.UserAssignedIdentities) > 0 {
				if vm.Identity.UserAssignedIdentities == nil {
					vm.Identity.UserAssignedIdentities = make(map[string]*armcompute.UserAssignedIdentitiesValue)
				}
				for id, val := range updates.Identity.UserAssignedIdentities {
					vm.Identity.UserAssignedIdentities[id] = val
				}
			}
		}

		// Update the stored shape
		c.Instances.Store(id, vm)

		return &armcompute.VirtualMachinesClientUpdateResponse{VirtualMachine: vm}, nil
	})
}

func (c *VirtualMachinesAPI) Get(_ context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientGetOptions) (armcompute.VirtualMachinesClientGetResponse, error) {
	input := &VirtualMachineGetInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		Options:           options,
	}
	return c.VirtualMachineGetBehavior.Invoke(input, func(input *VirtualMachineGetInput) (armcompute.VirtualMachinesClientGetResponse, error) {
		instance, ok := c.Instances.Load(utils.MkVMID(input.ResourceGroupName, input.VMName))
		if !ok {
			return armcompute.VirtualMachinesClientGetResponse{}, &azcore.ResponseError{ErrorCode: errors.ResourceNotFound}
		}
		return armcompute.VirtualMachinesClientGetResponse{
			VirtualMachine: instance.(armcompute.VirtualMachine),
		}, nil
	})
}

func (c *VirtualMachinesAPI) BeginDelete(_ context.Context, resourceGroupName string, vmName string, options *armcompute.VirtualMachinesClientBeginDeleteOptions) (*runtime.Poller[armcompute.VirtualMachinesClientDeleteResponse], error) {
	input := &VirtualMachineDeleteInput{
		ResourceGroupName: resourceGroupName,
		VMName:            vmName,
		Options:           options,
	}
	return c.VirtualMachineDeleteBehavior.Invoke(input, func(input *VirtualMachineDeleteInput) (*armcompute.VirtualMachinesClientDeleteResponse, error) {
		c.Instances.Delete(utils.MkVMID(input.ResourceGroupName, input.VMName))
		return &armcompute.VirtualMachinesClientDeleteResponse{}, nil
	})
}
