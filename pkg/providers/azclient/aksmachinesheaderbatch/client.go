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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

type AKSMachinesHeaderBatchAPI interface {
	BeginCreateWithBatch(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, machine *armcontainerservice.Machine) error
}

// We don't need the rest of machine API interface. Just create.
type AKSMachinesCreateAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, parameters armcontainerservice.Machine, options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error)
}

// Client implements AKSMachinesHeaderBatchAPI by coalescing concurrent
// BeginCreateWithBatch calls through a generic batcher. Requests are grouped
// by resource path + template hash and dispatched to the executor, which
// calls AKSMachinesCreateAPI.BeginCreateOrUpdate with the BatchPutMachine header.
type Client struct {
	b *batcher.Batcher[aksMachineCreatePayload, struct{}]
}

func NewClient(ctx context.Context, aksMachinesClient AKSMachinesCreateAPI, opts batcher.Options) *Client {
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
	machine *armcontainerservice.Machine,
) error {
	select {
	case response := <-c.b.Enqueue(aksMachineCreatePayload{
		resourceGroupName: resourceGroupName,
		resourceName:      resourceName,
		agentPoolName:     agentPoolName,
		machineName:       machineName,
		machineBody:       machine,
	}):
		return response.Err
	case <-ctx.Done():
		// WARNING: cancelling context does not cancel Enqueue call and batch execution.
		// It only prevents the caller from waiting for the response. Created resources may still exist, but will be garbage collected as they don't have NodeClaim.
		return ctx.Err()
	}
}
