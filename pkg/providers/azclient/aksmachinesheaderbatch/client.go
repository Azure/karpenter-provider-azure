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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

type AKSMachinesHeaderBatchAPI interface {
	// BeginCreateWithBatch submits a single machine creation into the batch system.
	// Returns:
	//   - (*HandlableError, nil):  handlable error — the machine was not created
	//   - (nil, error):      		operational error (e.g., parsing failure)
	//   - (nil, nil):        		success — machine creation started
	// Design note: the separation of errors is a result of:
	// - The different formats of the error that the API returned
	// - What the caller likely wants to do with them (e.g., look at code + message for
	//   API errors, but nothing else).
	// - The fact that HandlableError is considered one of the "expected" states in this
	//   context, just not ideal. Operational error, on the other hand, is more of a bug.
	//
	// useWindowsGen2VM requests a Generation 2 Windows image from the RP (UseWindowsGen2VM header).
	// It participates in the batch key, so only machines that agree on it coalesce into one request.
	BeginCreateWithBatch(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, machine *armcontainerservice.Machine, useWindowsGen2VM bool) (*offerings.HandlableError, error)
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
	b *batcher.Batcher[aksMachineCreatePayload, *offerings.HandlableError]
}

func NewClient(ctx context.Context, aksMachinesClient AKSMachinesCreateAPI, opts batcher.Options) *Client {
	exec := newExecutor(aksMachinesClient)

	b := batcher.New[aksMachineCreatePayload, *offerings.HandlableError](
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
	useWindowsGen2VM bool,
) (*offerings.HandlableError, error) {
	responseChan, err := c.b.Enqueue(aksMachineCreatePayload{
		resourceGroupName: resourceGroupName,
		resourceName:      resourceName,
		agentPoolName:     agentPoolName,
		machineName:       machineName,
		machineBody:       machine,
		useWindowsGen2VM:  useWindowsGen2VM,
	})
	if err != nil {
		return nil, err
	}

	select {
	case response := <-responseChan:
		return response.Payload, response.Err
	case <-ctx.Done():
		// WARNING: canceling context does not cancel Enqueue call and batch execution.
		// It only prevents the caller from waiting for the response. Created resources may still exist, but will be garbage collected as they don't have NodeClaim.
		// Suggestion: add canceling mechanism? But may not worth working on?
		return nil, ctx.Err()
	}
}
