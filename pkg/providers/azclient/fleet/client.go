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
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

// Client coalesces concurrent single-VM Fleet requests with identical bodies into Fleet creates.
type Client struct {
	b *batcher.Batcher[FleetVMProvisionRequest, FleetBatchResponse]
}

// NewClient creates a Fleet batch client and starts its batching loop, which runs until ctx is done.
func NewClient(ctx context.Context, fleetClient azapi.FleetsAPI, clusterName, resourceGroup string, opts batcher.Options) *Client {
	exec := newExecutor(fleetClient, clusterName, resourceGroup)
	b := batcher.New(ctx, prepareRequestBatchKey, exec.executeBatch, opts)
	b.Start()
	return &Client{b: b}
}

// CreateVM enqueues one NodeClaim's request and blocks until its batch is executed or ctx is done.
// Once enqueued, a request stays in its batch even if ctx is canceled.
func (c *Client) CreateVM(ctx context.Context, req FleetVMProvisionRequest) FleetBatchResponse {
	respChan, err := c.b.Enqueue(req)
	if err != nil {
		return FleetBatchResponse{Error: fmt.Errorf("enqueueing fleet request: %w", err)}
	}
	select {
	case <-ctx.Done():
		return FleetBatchResponse{Error: ctx.Err()}
	case resp := <-respChan:
		if resp.Err != nil {
			return FleetBatchResponse{Error: resp.Err}
		}
		return resp.Payload
	}
}
