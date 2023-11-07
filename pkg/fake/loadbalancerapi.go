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
	"github.com/Azure/karpenter/pkg/providers/loadbalancer"
	"github.com/samber/lo"
)

type LoadBalancersBehavior struct {
	LoadBalancers sync.Map
}

// assert that the fake implements the interface
var _ loadbalancer.LoadBalancersAPI = &LoadBalancersAPI{}

type LoadBalancersAPI struct {
	LoadBalancersBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (api *LoadBalancersAPI) Reset() {
	api.LoadBalancers.Range(func(k, v any) bool {
		api.LoadBalancers.Delete(k)
		return true
	})
}

func (api *LoadBalancersAPI) Get(_ context.Context, resourceGroupName string, loadBalancerName string, _ *armnetwork.LoadBalancersClientGetOptions) (armnetwork.LoadBalancersClientGetResponse, error) {
	id := MakeLoadBalancerID(resourceGroupName, loadBalancerName)
	lb, ok := api.LoadBalancers.Load(id)
	if !ok {
		return armnetwork.LoadBalancersClientGetResponse{}, fmt.Errorf("not found")
	}
	return armnetwork.LoadBalancersClientGetResponse{
		LoadBalancer: lb.(armnetwork.LoadBalancer),
	}, nil
}

func (api *LoadBalancersAPI) NewListPager(_ string, _ *armnetwork.LoadBalancersClientListOptions) *runtime.Pager[armnetwork.LoadBalancersClientListResponse] {
	pagingHandler := runtime.PagingHandler[armnetwork.LoadBalancersClientListResponse]{
		More: func(page armnetwork.LoadBalancersClientListResponse) bool {
			return false // TODO: It might be ideal if we had a MockPager which sometimes simulated multiple pages of results to ensure we handle that correctly
		},
		Fetcher: func(ctx context.Context, _ *armnetwork.LoadBalancersClientListResponse) (armnetwork.LoadBalancersClientListResponse, error) {
			output := armnetwork.LoadBalancerListResult{
				Value: []*armnetwork.LoadBalancer{},
			}
			api.LoadBalancers.Range(func(key, value any) bool {
				cast := value.(armnetwork.LoadBalancer)
				output.Value = append(output.Value, &cast)

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
	}
	return runtime.NewPager(pagingHandler)
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
