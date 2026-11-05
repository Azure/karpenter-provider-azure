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
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	fakesync "github.com/Azure/karpenter-provider-azure/pkg/fake/sync"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/samber/lo"
)

type PublicIPCreateInput struct {
	ResourceGroup string
	Name          string
	PublicIP      armnetwork.PublicIPAddress
}

type PublicIPDeleteInput struct {
	ResourceGroup string
	Name          string
}

type PublicIPAddressesAPI struct {
	Addresses      fakesync.Map[string, armnetwork.PublicIPAddress]
	CreateBehavior MockedLRO[PublicIPCreateInput, armnetwork.PublicIPAddressesClientCreateOrUpdateResponse]
	DeleteBehavior MockedLRO[PublicIPDeleteInput, armnetwork.PublicIPAddressesClientDeleteResponse]
}

var _ azapi.PublicIPAddressesAPI = (*PublicIPAddressesAPI)(nil)

func (api *PublicIPAddressesAPI) Get(_ context.Context, group, name string, _ *armnetwork.PublicIPAddressesClientGetOptions) (armnetwork.PublicIPAddressesClientGetResponse, error) {
	address, ok := api.Addresses.Load(group + "/" + name)
	if !ok {
		return armnetwork.PublicIPAddressesClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	return armnetwork.PublicIPAddressesClientGetResponse{PublicIPAddress: address}, nil
}

func (api *PublicIPAddressesAPI) BeginCreateOrUpdate(_ context.Context, group, name string, address armnetwork.PublicIPAddress, _ *armnetwork.PublicIPAddressesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armnetwork.PublicIPAddressesClientCreateOrUpdateResponse], error) {
	return api.CreateBehavior.Invoke(&PublicIPCreateInput{ResourceGroup: group, Name: name, PublicIP: address}, func(input *PublicIPCreateInput) (*armnetwork.PublicIPAddressesClientCreateOrUpdateResponse, error) {
		result := input.PublicIP
		result.Name = &input.Name
		result.ID = lo.ToPtr(fmt.Sprintf("/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Network/publicIPAddresses/%s", input.ResourceGroup, input.Name))
		result.Properties.IPAddress = lo.ToPtr("203.0.113.10")
		api.Addresses.Store(input.ResourceGroup+"/"+input.Name, result)
		return &armnetwork.PublicIPAddressesClientCreateOrUpdateResponse{PublicIPAddress: result}, nil
	})
}

func (api *PublicIPAddressesAPI) BeginDelete(_ context.Context, group, name string, _ *armnetwork.PublicIPAddressesClientBeginDeleteOptions) (*runtime.Poller[armnetwork.PublicIPAddressesClientDeleteResponse], error) {
	return api.DeleteBehavior.Invoke(&PublicIPDeleteInput{ResourceGroup: group, Name: name}, func(input *PublicIPDeleteInput) (*armnetwork.PublicIPAddressesClientDeleteResponse, error) {
		api.Addresses.Delete(input.ResourceGroup + "/" + input.Name)
		return &armnetwork.PublicIPAddressesClientDeleteResponse{}, nil
	})
}

func (api *PublicIPAddressesAPI) NewListPager(_ string, _ *armnetwork.PublicIPAddressesClientListOptions) *runtime.Pager[armnetwork.PublicIPAddressesClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[armnetwork.PublicIPAddressesClientListResponse]{
		More: func(armnetwork.PublicIPAddressesClientListResponse) bool { return false },
		Fetcher: func(context.Context, *armnetwork.PublicIPAddressesClientListResponse) (armnetwork.PublicIPAddressesClientListResponse, error) {
			addresses := []*armnetwork.PublicIPAddress{}
			api.Addresses.Range(func(_ string, address armnetwork.PublicIPAddress) bool {
				addresses = append(addresses, &address)
				return true
			})
			return armnetwork.PublicIPAddressesClientListResponse{PublicIPAddressListResult: armnetwork.PublicIPAddressListResult{Value: addresses}}, nil
		},
	})
}

func (api *PublicIPAddressesAPI) Reset() {
	api.Addresses.Clear()
	api.CreateBehavior.Reset()
	api.DeleteBehavior.Reset()
}
