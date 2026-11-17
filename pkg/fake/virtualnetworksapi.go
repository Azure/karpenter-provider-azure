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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/samber/lo"
)

type VirtualNetworksAPI struct {
	GetFunc func(ctx context.Context, resourceGroupName string, virtualNetworkName string, options *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error)
}

var _ azapi.VirtualNetworksAPI = &VirtualNetworksAPI{}

func (v *VirtualNetworksAPI) Get(ctx context.Context, resourceGroupName string, virtualNetworkName string, options *armnetwork.VirtualNetworksClientGetOptions) (armnetwork.VirtualNetworksClientGetResponse, error) {
	if v.GetFunc != nil {
		return v.GetFunc(ctx, resourceGroupName, virtualNetworkName, options)
	}
	return armnetwork.VirtualNetworksClientGetResponse{
		VirtualNetwork: armnetwork.VirtualNetwork{
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				ResourceGUID: lo.ToPtr("00000000-0000-0000-0000-000000000000"),
			},
		},
	}, nil
}

func (v *VirtualNetworksAPI) Reset() {
	v.GetFunc = nil
}
