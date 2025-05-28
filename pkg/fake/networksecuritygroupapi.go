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
	"sort"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/networksecuritygroup"
	"github.com/samber/lo"
)

type NetworkSecurityGroupBevhavior struct {
	NSGs sync.Map
}

// assert that the fake implements the interface
var _ networksecuritygroup.API = &NetworkSecurityGroupAPI{}

type NetworkSecurityGroupAPI struct {
	NetworkSecurityGroupBevhavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (api *NetworkSecurityGroupAPI) Reset() {
	api.NSGs.Range(func(k, v any) bool {
		api.NSGs.Delete(k)
		return true
	})
}

// Get implements networksecuritygroup.API.
func (api *NetworkSecurityGroupAPI) Get(
	ctx context.Context,
	resourceGroupName string,
	securityGroupName string,
	options *armnetwork.SecurityGroupsClientGetOptions,
) (armnetwork.SecurityGroupsClientGetResponse, error) {
	id := MakeNetworkSecurityGroupID(resourceGroupName, securityGroupName)
	nsg, ok := api.NSGs.Load(id)
	if !ok {
		return armnetwork.SecurityGroupsClientGetResponse{}, fmt.Errorf("not found")
	}
	return armnetwork.SecurityGroupsClientGetResponse{
		SecurityGroup: nsg.(armnetwork.SecurityGroup),
	}, nil
}

// NewListPager implements networksecuritygroup.API.
func (api *NetworkSecurityGroupAPI) NewListPager(resourceGroupName string, options *armnetwork.SecurityGroupsClientListOptions) *runtime.Pager[armnetwork.SecurityGroupsClientListResponse] {
	pagingHandler := runtime.PagingHandler[armnetwork.SecurityGroupsClientListResponse]{
		More: func(page armnetwork.SecurityGroupsClientListResponse) bool {
			return false // TODO: It might be ideal if we had a MockPager which sometimes simulated multiple pages of results to ensure we handle that correctly
		},
		Fetcher: func(ctx context.Context, _ *armnetwork.SecurityGroupsClientListResponse) (armnetwork.SecurityGroupsClientListResponse, error) {
			output := armnetwork.SecurityGroupListResult{
				Value: []*armnetwork.SecurityGroup{},
			}
			api.NSGs.Range(func(key, value any) bool {
				cast := value.(armnetwork.SecurityGroup)
				output.Value = append(output.Value, &cast)

				return true
			})

			// Sort the result according to ID so that we have a stable base to write asserts upon
			sort.Slice(output.Value, func(i, j int) bool {
				l := output.Value[i]
				r := output.Value[j]

				return lo.FromPtr(l.ID) < lo.FromPtr(r.ID)
			})

			return armnetwork.SecurityGroupsClientListResponse{
				SecurityGroupListResult: output,
			}, nil
		},
	}
	return runtime.NewPager(pagingHandler)
}

func MakeNetworkSecurityGroupID(resourceGroupName, networkSecurityGroupName string) string {
	const subscriptionID = "12345678-1234-1234-1234-123456789012" // TODO: This is duplicated from other places, we should consider putting it in a common place
	const idFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s"

	return fmt.Sprintf(idFormat, subscriptionID, resourceGroupName, networkSecurityGroupName)
}
