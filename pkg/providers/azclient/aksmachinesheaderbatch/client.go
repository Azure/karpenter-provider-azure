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

package aksmachinesheaderbatch

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

// Client implements AKSMachinesBatchAPI by coalescing concurrent
// BeginCreateWithBatch calls through a generic batcher. Requests are grouped
// by resource path + template hash and dispatched to the executor, which
// calls AKSMachinesAPI.BeginCreateOrUpdate with the BatchPutMachine header.
type Client struct {
	b *batcher.Batcher[aksMachineCreatePayload, struct{}]
}

func NewClient(ctx context.Context, aksMachinesClient azapi.AKSMachinesAPI, opts batcher.Options) *Client {
	exec := newExecutor(aksMachinesClient)

	b := batcher.New[aksMachineCreatePayload, struct{}](
		ctx,
		determineBatchKey,
		exec.executeBatch,
		opts,
	)
	b.Start()

	return &Client{b: b}
}

// BeginCreateWithBatch submits a single machine creation into the batch system.
// The call blocks until the batch window fires and the API call completes.
func (c *Client) BeginCreateWithBatch(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	machineName string,
	template armcontainerservice.Machine,
) error {
	req := &batcher.BatchedRequest[aksMachineCreatePayload, struct{}]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: resourceGroupName,
			resourceName:      resourceName,
			agentPoolName:     agentPoolName,
			machineName:       machineName,
			machineBody:       template,
		},
		ResponseChan: make(chan *batcher.Response[struct{}], 1),
	}

	select {
	case response := <-c.b.Enqueue(req):
		return response.Err
	case <-ctx.Done():
		return ctx.Err()
	}
}
