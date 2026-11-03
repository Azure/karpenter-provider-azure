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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	fakesync "github.com/Azure/karpenter-provider-azure/pkg/fake/sync"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/samber/lo"
)

type LoadBalancerListInput struct {
	ResourceGroupName string
	Options           *armnetwork.LoadBalancersClientListOptions
}

type LoadBalancersBehavior struct {
	LoadBalancers        fakesync.Map[string, armnetwork.LoadBalancer]
	NewListPagerBehavior MockedFunction[LoadBalancerListInput, *runtime.Pager[armnetwork.LoadBalancersClientListResponse]]
}

// assert that the fake implements the interface
var _ loadbalancer.LoadBalancersAPI = &LoadBalancersAPI{}

type LoadBalancersAPI struct {
	LoadBalancersBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (api *LoadBalancersAPI) Reset() {
	api.LoadBalancers.Clear()
	api.NewListPagerBehavior.Reset()
}

func (api *LoadBalancersAPI) Get(_ context.Context, resourceGroupName string, loadBalancerName string, _ *armnetwork.LoadBalancersClientGetOptions) (armnetwork.LoadBalancersClientGetResponse, error) {
	id := MakeLoadBalancerID(resourceGroupName, loadBalancerName)
	lb, ok := api.LoadBalancers.Load(id)
	if !ok {
		return armnetwork.LoadBalancersClientGetResponse{}, fmt.Errorf("not found")
	}
	return armnetwork.LoadBalancersClientGetResponse{
		LoadBalancer: lb,
	}, nil
}

func (api *LoadBalancersAPI) NewListPager(resourceGroupName string, options *armnetwork.LoadBalancersClientListOptions) *runtime.Pager[armnetwork.LoadBalancersClientListResponse] {
	input := &LoadBalancerListInput{
		ResourceGroupName: resourceGroupName,
		Options:           options,
	}
	pager, _ := api.NewListPagerBehavior.Invoke(input, func(_ *LoadBalancerListInput) (*runtime.Pager[armnetwork.LoadBalancersClientListResponse], error) {
		p := runtime.NewPager(runtime.PagingHandler[armnetwork.LoadBalancersClientListResponse]{
			More: func(page armnetwork.LoadBalancersClientListResponse) bool {
				return false // TODO: It might be ideal if we had a MockPager which sometimes simulated multiple pages of results to ensure we handle that correctly
			},
			Fetcher: func(ctx context.Context, _ *armnetwork.LoadBalancersClientListResponse) (armnetwork.LoadBalancersClientListResponse, error) {
				output := armnetwork.LoadBalancerListResult{
					Value: []*armnetwork.LoadBalancer{},
				}
				api.LoadBalancers.Range(func(key string, value armnetwork.LoadBalancer) bool {
					output.Value = append(output.Value, &value)
					return true
				})

				// Sort the result according to ID so that we have a stable base to write asserts upon
				sort.Slice(output.Value, func(i, j int) bool {
					l := output.Value[i]
					r := output.Value[j]
					return lo.FromPtr(l.ID) < lo.FromPtr(r.ID)
				})

				return armnetwork.LoadBalancersClientListResponse{
					LoadBalancerListResult: output,
				}, nil
			},
		})
		return p, nil
	})
	return pager
}

func MakeLoadBalancerID(resourceGroupName, loadBalancerName string) string {
	const subscriptionID = "subscriptionID" // not important for fake
	const idFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers/%s"

	return fmt.Sprintf(idFormat, subscriptionID, resourceGroupName, loadBalancerName)
}

func MakeBackendAddressPoolID(resourceGroupName, loadBalancerName string, backendAddressPoolName string) string {
	const subscriptionID = "subscriptionID" // not important for fake
	const idFormat = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers/%s/backendAddressPools/%s"

	return fmt.Sprintf(idFormat, subscriptionID, resourceGroupName, loadBalancerName, backendAddressPoolName)
}
