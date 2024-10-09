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
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

type VirtualMachineExtensionCreateOrUpdateInput struct {
	ResourceGroupName           string
	VirtualMachineName          string
	VirtualMachineExtensionName string
	VirtualMachineExtension     armcompute.VirtualMachineExtension
	Options                     *armcompute.VirtualMachineExtensionsClientBeginCreateOrUpdateOptions
}

type VirtualMachineExtensionsBehavior struct {
	VirtualMachineExtensionsCreateOrUpdateBehavior MockedLRO[VirtualMachineExtensionCreateOrUpdateInput, armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse]
	VMExtensions                                   sync.Map
}

// assert that ComputeAPI implements ARMComputeAPI
var _ instance.VirtualMachineExtensionsAPI = &VirtualMachineExtensionsAPI{}

type VirtualMachineExtensionsAPI struct {
	// instance.VirtualMachineExtensionsAPI
	VirtualMachineExtensionsBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *VirtualMachineExtensionsAPI) Reset() {
	c.VirtualMachineExtensionsCreateOrUpdateBehavior.Reset()
}

func (c *VirtualMachineExtensionsAPI) BeginCreateOrUpdate(_ context.Context, resourceGroupName, vmName, extensionName string, extension armcompute.VirtualMachineExtension, options *armcompute.VirtualMachineExtensionsClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse], error) {
	input := &VirtualMachineExtensionCreateOrUpdateInput{
		ResourceGroupName:           resourceGroupName,
		VirtualMachineName:          vmName,
		VirtualMachineExtensionName: extensionName,
		VirtualMachineExtension:     extension,
		Options:                     options,
	}

	return c.VirtualMachineExtensionsCreateOrUpdateBehavior.Invoke(input, func(input *VirtualMachineExtensionCreateOrUpdateInput) (*armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse, error) {
		vmExtension := input.VirtualMachineExtension
		id := mkVMExtensionID(input.ResourceGroupName, input.VirtualMachineName, input.VirtualMachineExtensionName)
		vmExtension.ID = to.StringPtr(id)
		c.VMExtensions.Store(id, vmExtension)
		return &armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse{
			VirtualMachineExtension: vmExtension,
		}, nil
	})
}

func (c *VirtualMachineExtensionsAPI) Get(ctx context.Context, resourceGroupName string, vmName string, vmExtensionName string, options *armcompute.VirtualMachineExtensionsClientGetOptions) (armcompute.VirtualMachineExtensionsClientGetResponse, error) {
	id := mkVMExtensionID(resourceGroupName, vmName, vmExtensionName)
	vmExtension, ok := c.VMExtensions.Load(id)
	if !ok {
		return armcompute.VirtualMachineExtensionsClientGetResponse{}, &azcore.ResponseError{ErrorCode: errors.ResourceNotFound}
	}
	return armcompute.VirtualMachineExtensionsClientGetResponse{
		VirtualMachineExtension: vmExtension.(armcompute.VirtualMachineExtension),
	}, nil
}

func mkVMExtensionID(resourceGroupName, vmName, extensionName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/extensions/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName, extensionName)
}
