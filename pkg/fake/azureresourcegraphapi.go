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
	"encoding/json"

	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
)

type AzureResourceGraphResourcesInput struct {
	Query   armresourcegraph.QueryRequest
	Options *armresourcegraph.ClientResourcesOptions
}

type AzureResourceGraphBehavior struct {
	AzureResourceGraphResourcesBehavior MockedFunction[AzureResourceGraphResourcesInput, armresourcegraph.ClientResourcesResponse]
	VirtualMachinesAPI                  *VirtualMachinesAPI
	ResourceGroup                       string
}

// assert that the fake implements the interface
var _ instance.AzureResourceGraphAPI = &AzureResourceGraphAPI{}

type AzureResourceGraphAPI struct {
	AzureResourceGraphBehavior
}

// Reset must be called between tests otherwise tests will pollute each other.
func (c *AzureResourceGraphAPI) Reset() {}

func (c *AzureResourceGraphAPI) Resources(_ context.Context, query armresourcegraph.QueryRequest, options *armresourcegraph.ClientResourcesOptions) (armresourcegraph.ClientResourcesResponse, error) {
	input := &AzureResourceGraphResourcesInput{
		Query:   query,
		Options: options,
	}
	resourceList := c.getResourceList(*query.Query)

	return c.AzureResourceGraphResourcesBehavior.Invoke(input, func(input *AzureResourceGraphResourcesInput) (armresourcegraph.ClientResourcesResponse, error) {
		return armresourcegraph.ClientResourcesResponse{
			QueryResponse: armresourcegraph.QueryResponse{
				Data: resourceList,
			},
		}, nil
	})
}

func (c *AzureResourceGraphAPI) getResourceList(query string) []interface{} {
	switch query {
	case instance.GetListQueryBuilder(c.ResourceGroup).String():
		vmList := lo.Filter(c.loadVMObjects(), func(vm armcompute.VirtualMachine, _ int) bool {
			return vm.Tags != nil && vm.Tags[instance.NodePoolTagKey] != nil
		})
		resourceList := lo.Map(vmList, func(vm armcompute.VirtualMachine, _ int) interface{} {
			b, _ := json.Marshal(vm)
			return convertBytesToInterface(b)
		})
		return resourceList
	}
	return nil
}

func (c *AzureResourceGraphAPI) loadVMObjects() []armcompute.VirtualMachine {
	vmList := []armcompute.VirtualMachine{}
	c.VirtualMachinesAPI.Instances.Range(func(k, v any) bool {
		vm, _ := c.VirtualMachinesAPI.Instances.Load(k)
		vmList = append(vmList, vm.(armcompute.VirtualMachine))
		return true
	})
	return vmList
}

func convertBytesToInterface(b []byte) interface{} {
	jsonObj := instance.Resource{}
	_ = json.Unmarshal(b, &jsonObj)
	return interface{}(jsonObj)
}
