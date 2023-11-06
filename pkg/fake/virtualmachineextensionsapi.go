// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fake

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter/pkg/providers/instance"
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
var _ instance.VirtualMachineExtensionsAPI = (*VirtualMachineExtensionsAPI)(nil)

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
		result.ID = to.StringPtr(mkVMExtensionID(input.ResourceGroupName, input.VirtualMachineName, input.VirtualMachineExtensionName))
		return &armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse{
			VirtualMachineExtension: result,
		}, nil
	})
}

func mkVMExtensionID(resourceGroupName, vmName, extensionName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/extensions/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName, extensionName)
}
