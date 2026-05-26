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

package fleet

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

// Client is the Fleet batch client. It coalesces concurrent BeginCreateWithFleet calls
// through a generic batcher, grouping by batch key hash.
type Client struct {
	b *batcher.Batcher[FleetCreateRequest, FleetBatchResponse]
}

// NewClient creates a Fleet batch client and starts the batcher loop.
func NewClient(
	ctx context.Context,
	fleetClient FleetAPI,
	vmClient VMAPI,
	errorHandler *offerings.FleetErrorHandler,
	clusterName, resourceGroup, subscription, location string,
	opts batcher.Options,
) *Client {
	exec := newExecutor(fleetClient, vmClient, errorHandler, clusterName, resourceGroup, location)

	b := batcher.New[FleetCreateRequest, FleetBatchResponse](
		ctx,
		DetermineBatchKey,
		exec.executeBatch,
		opts,
	)
	b.Start()

	return &Client{b: b}
}

// BeginCreateWithFleet submits a single NodeClaim request into the fleet batching system.
// Returns when the batch fires and the caller's VM is assigned (or an error occurs).
func (c *Client) BeginCreateWithFleet(ctx context.Context, req FleetCreateRequest) FleetBatchResponse {
	// TODO: enqueue into c.b, wait on response channel, return response
	return FleetBatchResponse{}
}
