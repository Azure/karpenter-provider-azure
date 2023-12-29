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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/Azure/karpenter/pkg/providers/instance"
)

type NetworkInterfaceCreateOrUpdateInput struct {
	ResourceGroupName string
	InterfaceName     string
	Interface         armnetwork.Interface
	Options           *armnetwork.InterfacesClientBeginCreateOrUpdateOptions
}

type NetworkInterfaceDeleteInput struct {
	ResourceGroupName, InterfaceName string
}

type NetworkInterfacesBehavior struct {
	NetworkInterfacesCreateOrUpdateBehavior MockedLRO[NetworkInterfaceCreateOrUpdateInput, armnetwork.InterfacesClientCreateOrUpdateResponse]
	NetworkInterfacesDeleteBehavior         MockedLRO[NetworkInterfaceDeleteInput, armnetwork.InterfacesClientDeleteResponse]
	NetworkInterfaces                       sync.Map
}

// assert that the fake implements the interface
var _ instance.NetworkInterfacesAPI = (*NetworkInterfacesAPI)(nil)

type NetworkInterfacesAPI struct {
	// instance.NetworkInterfacesAPI
	NetworkInterfacesBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *NetworkInterfacesAPI) Reset() {
	c.NetworkInterfacesCreateOrUpdateBehavior.Reset()
	c.NetworkInterfaces.Range(func(k, v any) bool {
		c.NetworkInterfaces.Delete(k)
		return true
	})
}

func (c *NetworkInterfacesAPI) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, interfaceName string, iface armnetwork.Interface, options *armnetwork.InterfacesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armnetwork.InterfacesClientCreateOrUpdateResponse], error) {
	input := &NetworkInterfaceCreateOrUpdateInput{
		ResourceGroupName: resourceGroupName,
		InterfaceName:     interfaceName,
		Interface:         iface,
		Options:           options,
	}

	return c.NetworkInterfacesCreateOrUpdateBehavior.Invoke(input, func(input *NetworkInterfaceCreateOrUpdateInput) (*armnetwork.InterfacesClientCreateOrUpdateResponse, error) {
		iface := input.Interface
		id := mkNetworkInterfaceID(input.ResourceGroupName, input.InterfaceName)
		iface.ID = to.StringPtr(id)
		c.NetworkInterfaces.Store(id, iface)
		return &armnetwork.InterfacesClientCreateOrUpdateResponse{
			Interface: iface,
		}, nil
	})
}

func (c *NetworkInterfacesAPI) Get(_ context.Context, resourceGroupName string, interfaceName string, _ *armnetwork.InterfacesClientGetOptions) (armnetwork.InterfacesClientGetResponse, error) {
	id := mkNetworkInterfaceID(resourceGroupName, interfaceName)
	iface, ok := c.NetworkInterfaces.Load(id)
	if !ok {
		return armnetwork.InterfacesClientGetResponse{}, &azcore.ResponseError{ErrorCode: errors.ResourceNotFound}
	}
	return armnetwork.InterfacesClientGetResponse{
		Interface: iface.(armnetwork.Interface),
	}, nil
}

func (c *NetworkInterfacesAPI) BeginDelete(_ context.Context, resourceGroupName string, interfaceName string, _ *armnetwork.InterfacesClientBeginDeleteOptions) (*runtime.Poller[armnetwork.InterfacesClientDeleteResponse], error) {
	input := &NetworkInterfaceDeleteInput{
		ResourceGroupName: resourceGroupName,
		InterfaceName:     interfaceName,
	}
	return c.NetworkInterfacesDeleteBehavior.Invoke(input, func(input *NetworkInterfaceDeleteInput) (*armnetwork.InterfacesClientDeleteResponse, error) {
		id := mkNetworkInterfaceID(resourceGroupName, interfaceName)
		c.NetworkInterfaces.Delete(id)
		return &armnetwork.InterfacesClientDeleteResponse{}, nil
	})
}

func mkNetworkInterfaceID(resourceGroupName, interfaceName string) string {
	const subscriptionID = "subscriptionID" // not important for fake
	const idFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s"
	return fmt.Sprintf(idFormat, subscriptionID, resourceGroupName, interfaceName)
}
