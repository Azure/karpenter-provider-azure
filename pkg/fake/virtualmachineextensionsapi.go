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

	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
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
	// not keeping track of extensions
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
		result := input.VirtualMachineExtension
		result.ID = lo.ToPtr(mkVMExtensionID(input.ResourceGroupName, input.VirtualMachineName, input.VirtualMachineExtensionName))
		return &armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse{
			VirtualMachineExtension: result,
		}, nil
	})
}

func mkVMExtensionID(resourceGroupName, vmName, extensionName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/extensions/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName, extensionName)
}
