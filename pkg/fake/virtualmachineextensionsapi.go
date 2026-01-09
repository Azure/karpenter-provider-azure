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
	"maps"
	"net/http"
	"sync"

	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

type VirtualMachineExtensionCreateOrUpdateInput struct {
	ResourceGroupName           string
	VirtualMachineName          string
	VirtualMachineExtensionName string
	VirtualMachineExtension     armcompute.VirtualMachineExtension
	Options                     *armcompute.VirtualMachineExtensionsClientBeginCreateOrUpdateOptions
}

type VirtualMachineExtensionUpdateInput struct {
	ResourceGroupName             string
	VirtualMachineName            string
	VirtualMachineExtensionName   string
	VirtualMachineExtensionUpdate armcompute.VirtualMachineExtensionUpdate
	Options                       *armcompute.VirtualMachineExtensionsClientBeginUpdateOptions
}

type VirtualMachineExtensionsBehavior struct {
	VirtualMachineExtensionsCreateOrUpdateBehavior MockedLRO[VirtualMachineExtensionCreateOrUpdateInput, armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse]
	VirtualMachineExtensionsUpdateBehavior         MockedLRO[VirtualMachineExtensionUpdateInput, armcompute.VirtualMachineExtensionsClientUpdateResponse]
	Extensions                                     sync.Map
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
	c.VirtualMachineExtensionsUpdateBehavior.Reset()
	c.Extensions.Range(func(k, v any) bool {
		c.Extensions.Delete(k)
		return true
	})
}

func (c *VirtualMachineExtensionsAPI) BeginCreateOrUpdate(
	_ context.Context,
	resourceGroupName,
	vmName,
	extensionName string,
	extension armcompute.VirtualMachineExtension,
	options *armcompute.VirtualMachineExtensionsClientBeginCreateOrUpdateOptions,
) (*runtime.Poller[armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse], error) {
	input := &VirtualMachineExtensionCreateOrUpdateInput{
		ResourceGroupName:           resourceGroupName,
		VirtualMachineName:          vmName,
		VirtualMachineExtensionName: extensionName,
		VirtualMachineExtension:     extension,
		Options:                     options,
	}

	return c.VirtualMachineExtensionsCreateOrUpdateBehavior.Invoke(input, func(input *VirtualMachineExtensionCreateOrUpdateInput) (*armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse, error) {
		result := input.VirtualMachineExtension
		result.ID = lo.ToPtr(MakeVMExtensionID(input.ResourceGroupName, input.VirtualMachineName, input.VirtualMachineExtensionName))
		c.Extensions.Store(input.VirtualMachineExtensionName, result) // only store latest, but could be improved
		return &armcompute.VirtualMachineExtensionsClientCreateOrUpdateResponse{
			VirtualMachineExtension: result,
		}, nil
	})
}

func (c *VirtualMachineExtensionsAPI) BeginUpdate(
	_ context.Context,
	resourceGroupName string,
	vmName string,
	extensionName string,
	updates armcompute.VirtualMachineExtensionUpdate,
	options *armcompute.VirtualMachineExtensionsClientBeginUpdateOptions,
) (*runtime.Poller[armcompute.VirtualMachineExtensionsClientUpdateResponse], error) {
	input := &VirtualMachineExtensionUpdateInput{
		ResourceGroupName:             resourceGroupName,
		VirtualMachineName:            vmName,
		VirtualMachineExtensionName:   extensionName,
		VirtualMachineExtensionUpdate: updates,
		Options:                       options,
	}

	return c.VirtualMachineExtensionsUpdateBehavior.Invoke(input, func(input *VirtualMachineExtensionUpdateInput) (*armcompute.VirtualMachineExtensionsClientUpdateResponse, error) {
		result := input.VirtualMachineExtensionUpdate

		id := MakeVMExtensionID(input.ResourceGroupName, input.VirtualMachineName, input.VirtualMachineExtensionName)

		instance, ok := c.Extensions.Load(id)
		if !ok {
			return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
		}
		ext := instance.(armcompute.VirtualMachineExtension)

		if updates.Tags != nil {
			// VM tags are full-replace if they're specified
			ext.Tags = maps.Clone(updates.Tags)
		}

		c.Extensions.Store(input.VirtualMachineExtensionName, result)
		return &armcompute.VirtualMachineExtensionsClientUpdateResponse{
			VirtualMachineExtension: ext,
		}, nil
	})
}

func MakeVMExtensionID(resourceGroupName, vmName, extensionName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/extensions/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName, extensionName)
}
