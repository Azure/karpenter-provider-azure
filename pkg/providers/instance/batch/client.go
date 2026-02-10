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

package batch

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
)

type BatchingMachinesClient struct {
	realClient azclient.AKSMachinesAPI
	grouper    *Grouper

	resourceGroup string
	clusterName   string
	poolName      string
}

var _ azclient.AKSMachinesAPI = (*BatchingMachinesClient)(nil)

func NewBatchingMachinesClient(
	realClient azclient.AKSMachinesAPI,
	grouper *Grouper,
	resourceGroup string,
	clusterName string,
	poolName string,
) *BatchingMachinesClient {
	return &BatchingMachinesClient{
		realClient:    realClient,
		grouper:       grouper,
		resourceGroup: resourceGroup,
		clusterName:   clusterName,
		poolName:      poolName,
	}
}

func (c *BatchingMachinesClient) BeginCreateOrUpdate(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	aksMachineName string,
	parameters armcontainerservice.Machine,
	options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions,
) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	if !c.shouldBatch(ctx, options) {
		return c.realClient.BeginCreateOrUpdate(
			ctx, resourceGroupName, resourceName, agentPoolName,
			aksMachineName, parameters, options,
		)
	}

	req := &CreateRequest{
		ctx:           ctx,
		resourceGroup: resourceGroupName,
		clusterName:   resourceName,
		poolName:      agentPoolName,
		machineName:   aksMachineName,
		template:      parameters,
		responseChan:  make(chan *CreateResponse, 1),
	}

	var response *CreateResponse
	select {
	case response = <-c.grouper.EnqueueCreate(req):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if response.Err != nil {
		return nil, response.Err
	}

	// Return nil poller for batched operations.
	// The calling code should use the GET-based poller (aksmachinepoller) instead of the SDK poller.
	// This is because the SDK poller returned by batch is shared across all machines,
	// but we need to poll each machine's individual provisioning state.
	return nil, nil
}

func (c *BatchingMachinesClient) shouldBatch(
	ctx context.Context,
	options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions,
) bool {
	if !c.grouper.IsEnabled() {
		return false
	}

	if options != nil && options.IfMatch != nil {
		return false
	}

	if ShouldSkipBatching(ctx) {
		return false
	}

	return true
}

func (c *BatchingMachinesClient) Get(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	aksMachineName string,
	options *armcontainerservice.MachinesClientGetOptions,
) (armcontainerservice.MachinesClientGetResponse, error) {
	return c.realClient.Get(ctx, resourceGroupName, resourceName, agentPoolName, aksMachineName, options)
}

func (c *BatchingMachinesClient) NewListPager(
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	options *armcontainerservice.MachinesClientListOptions,
) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	return c.realClient.NewListPager(resourceGroupName, resourceName, agentPoolName, options)
}
