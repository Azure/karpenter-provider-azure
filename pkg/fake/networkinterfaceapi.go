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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
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

type NetworkInterfaceUpdateTagsInput struct {
	ResourceGroupName string
	InterfaceName     string
	Tags              armnetwork.TagsObject
	Options           *armnetwork.InterfacesClientUpdateTagsOptions
}

type NetworkInterfacesBehavior struct {
	NetworkInterfacesCreateOrUpdateBehavior MockedLRO[NetworkInterfaceCreateOrUpdateInput, armnetwork.InterfacesClientCreateOrUpdateResponse]
	NetworkInterfacesDeleteBehavior         MockedLRO[NetworkInterfaceDeleteInput, armnetwork.InterfacesClientDeleteResponse]
	NetworkInterfacesUpdateTagsBehavior     MockedFunction[NetworkInterfaceUpdateTagsInput, armnetwork.InterfacesClientUpdateTagsResponse]
	NetworkInterfaces                       sync.Map
}

// assert that the fake implements the interface
var _ azclient.NetworkInterfacesAPI = &NetworkInterfacesAPI{}

type NetworkInterfacesAPI struct {
	// azclient.NetworkInterfacesAPI
	NetworkInterfacesBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *NetworkInterfacesAPI) Reset() {
	c.NetworkInterfacesCreateOrUpdateBehavior.Reset()
	c.NetworkInterfacesDeleteBehavior.Reset()
	c.NetworkInterfacesUpdateTagsBehavior.Reset()
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
		iface.Name = lo.ToPtr(input.InterfaceName)
		id := MakeNetworkInterfaceID(input.ResourceGroupName, input.InterfaceName)
		iface.ID = lo.ToPtr(id)
		c.NetworkInterfaces.Store(id, iface)
		return &armnetwork.InterfacesClientCreateOrUpdateResponse{
			Interface: iface,
		}, nil
	})
}

func (c *NetworkInterfacesAPI) Get(_ context.Context, resourceGroupName string, interfaceName string, _ *armnetwork.InterfacesClientGetOptions) (armnetwork.InterfacesClientGetResponse, error) {
	id := MakeNetworkInterfaceID(resourceGroupName, interfaceName)
	iface, ok := c.NetworkInterfaces.Load(id)
	if !ok {
		return armnetwork.InterfacesClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound}
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
		id := MakeNetworkInterfaceID(input.ResourceGroupName, input.InterfaceName)
		c.NetworkInterfaces.Delete(id)
		return &armnetwork.InterfacesClientDeleteResponse{}, nil
	})
}

func (c *NetworkInterfacesAPI) UpdateTags(
	ctx context.Context,
	resourceGroupName string,
	interfaceName string,
	tags armnetwork.TagsObject,
	options *armnetwork.InterfacesClientUpdateTagsOptions,
) (armnetwork.InterfacesClientUpdateTagsResponse, error) {
	input := &NetworkInterfaceUpdateTagsInput{
		ResourceGroupName: resourceGroupName,
		InterfaceName:     interfaceName,
		Tags:              tags,
		Options:           options,
	}
	return c.NetworkInterfacesUpdateTagsBehavior.Invoke(input, func(input *NetworkInterfaceUpdateTagsInput) (armnetwork.InterfacesClientUpdateTagsResponse, error) {
		id := MakeNetworkInterfaceID(resourceGroupName, interfaceName)
		instance, ok := c.NetworkInterfaces.Load(id)
		if !ok {
			return armnetwork.InterfacesClientUpdateTagsResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound}
		}

		iface := instance.(armnetwork.Interface)

		if input.Tags.Tags != nil {
			// Tags are full-replace if they're specified
			iface.Tags = maps.Clone(input.Tags.Tags)
		}

		c.NetworkInterfaces.Store(id, iface)

		return armnetwork.InterfacesClientUpdateTagsResponse{
			Interface: iface,
		}, nil
	})
}

func MakeNetworkInterfaceID(resourceGroupName, interfaceName string) string {
	const subscriptionID = "subscriptionID" // not important for fake
	const idFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s"
	return fmt.Sprintf(idFormat, subscriptionID, resourceGroupName, interfaceName)
}
