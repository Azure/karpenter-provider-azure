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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

type AzureResourceGraphResourcesInput struct {
	Query   armresourcegraph.QueryRequest
	Options *armresourcegraph.ClientResourcesOptions
}

type AzureResourceGraphBehavior struct {
	AzureResourceGraphResourcesBehavior MockedFunction[AzureResourceGraphResourcesInput, armresourcegraph.ClientResourcesResponse]
	VirtualMachinesAPI                  *VirtualMachinesAPI
	NetworkInterfacesAPI                *NetworkInterfacesAPI
	ResourceGroup                       string
}

// assert that the fake implements the interface
var _ instance.AzureResourceGraphAPI = &AzureResourceGraphAPI{}

type AzureResourceGraphAPI struct {
	vmListQuery  string
	nicListQuery string
	AzureResourceGraphBehavior
}

func NewAzureResourceGraphAPI(resourceGroup string, virtualMachinesAPI *VirtualMachinesAPI, networkInterfacesAPI *NetworkInterfacesAPI) *AzureResourceGraphAPI {
	return &AzureResourceGraphAPI{
		vmListQuery:  instance.GetVMListQueryBuilder(resourceGroup).String(),
		nicListQuery: instance.GetNICListQueryBuilder(resourceGroup).String(),
		AzureResourceGraphBehavior: AzureResourceGraphBehavior{
			VirtualMachinesAPI:   virtualMachinesAPI,
			NetworkInterfacesAPI: networkInterfacesAPI,
			ResourceGroup:        resourceGroup,
		},
	}
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
	case c.vmListQuery:
		vmList := lo.Filter(c.loadVMObjects(), func(vm armcompute.VirtualMachine, _ int) bool {
			return vm.Tags != nil && vm.Tags[launchtemplate.NodePoolTagKey] != nil
		})
		resourceList := lo.Map(vmList, func(vm armcompute.VirtualMachine, _ int) interface{} {
			b, _ := json.Marshal(vm)
			return convertBytesToInterface(b)
		})
		return resourceList
	case c.nicListQuery:
		nicList := lo.Filter(c.loadNicObjects(), func(nic armnetwork.Interface, _ int) bool {
			return nic.Tags != nil && nic.Tags[launchtemplate.NodePoolTagKey] != nil
		})
		resourceList := lo.Map(nicList, func(nic armnetwork.Interface, _ int) interface{} {
			b, _ := json.Marshal(nic)
			return convertBytesToInterface(b)
		})
		return resourceList
	}
	return nil
}

func (c *AzureResourceGraphAPI) loadVMObjects() (vmList []armcompute.VirtualMachine) {
	c.VirtualMachinesAPI.Instances.Range(func(k, v any) bool {
		vm, _ := c.VirtualMachinesAPI.Instances.Load(k)
		vmList = append(vmList, vm.(armcompute.VirtualMachine))
		return true
	})
	return vmList
}

func (c *AzureResourceGraphAPI) loadNicObjects() (nicList []armnetwork.Interface) {
	c.NetworkInterfacesAPI.NetworkInterfaces.Range(func(k, v any) bool {
		nic, _ := c.NetworkInterfacesAPI.NetworkInterfaces.Load(k)
		nicList = append(nicList, nic.(armnetwork.Interface))
		return true
	})
	return nicList
}

func convertBytesToInterface(b []byte) interface{} {
	jsonObj := instance.Resource{}
	_ = json.Unmarshal(b, &jsonObj)
	return interface{}(jsonObj)
}
