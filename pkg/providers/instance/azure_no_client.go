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

package instance

import (
	"context"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

var agentPoolNotFoundRespError = &azcore.ResponseError{
	ErrorCode:  "NotFound",
	StatusCode: http.StatusNotFound,
}

// This "fake" client simulates the behavior of when there are no AKS machines present.
// There will be no outgoing calls.
// An example use case is when we want to cut the comms. with AKS machine API when we don't want to manage existing machines.
type noAKSMachinesClient struct{}

func NewNoAKSMachinesClient() AKSMachinesAPI {
	return &noAKSMachinesClient{}
}

func (d *noAKSMachinesClient) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, parameters armcontainerservice.Machine, options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	// As if agent pool is not found
	return nil, agentPoolNotFoundRespError
}

func (d *noAKSMachinesClient) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error) {
	// As if agent pool is not found
	return armcontainerservice.MachinesClientGetResponse{}, agentPoolNotFoundRespError
}

func (d *noAKSMachinesClient) NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	// As if agent pool is not found
	return runtime.NewPager(runtime.PagingHandler[armcontainerservice.MachinesClientListResponse]{
		More: func(armcontainerservice.MachinesClientListResponse) bool { return false },
		Fetcher: func(context.Context, *armcontainerservice.MachinesClientListResponse) (armcontainerservice.MachinesClientListResponse, error) {
			return armcontainerservice.MachinesClientListResponse{}, agentPoolNotFoundRespError
		},
	})
}

type noAKSAgentPoolsClient struct{}

// NewNoAKSAgentPoolsClient creates a new dry AKS agent pools client, attempting to create real client internally
func NewNoAKSAgentPoolsClient() AKSAgentPoolsAPI {
	return &noAKSAgentPoolsClient{}
}

func (d *noAKSAgentPoolsClient) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.AgentPoolsClientGetOptions) (armcontainerservice.AgentPoolsClientGetResponse, error) {
	// As if agent pool is not found
	return armcontainerservice.AgentPoolsClientGetResponse{}, agentPoolNotFoundRespError
}

func (d *noAKSAgentPoolsClient) BeginDeleteMachines(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachines armcontainerservice.AgentPoolDeleteMachinesParameter, options *armcontainerservice.AgentPoolsClientBeginDeleteMachinesOptions) (*runtime.Poller[armcontainerservice.AgentPoolsClientDeleteMachinesResponse], error) {
	// As if agent pool is not found
	return nil, agentPoolNotFoundRespError
}
