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
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// aksMachineCreatePayload is the per-request payload to be batched on.
type aksMachineCreatePayload struct {
	resourceGroupName string
	resourceName      string
	agentPoolName     string
	machineName       string
	machineBody       *armcontainerservice.Machine
}

// executor sends batches to Azure using the BatchPutMachine HTTP header.
// It transforms a pending batch into a single API call, then distributes
// per-machine results back to each request's channel.
type executor struct {
	realClient AKSMachinesCreateAPI
}

func newExecutor(realClient AKSMachinesCreateAPI) *executor {
	return &executor{realClient: realClient}
}

// executeBatch is the batcher.ExecuteBatch — it sends a batch to Azure as one
// API call, then distributes results back to each request's channel.
func (e *executor) executeBatch(ctx context.Context, batch *batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]) {
	log.FromContext(ctx).Info("executing an AKS machines header batch",
		"ID", batch.ID,
		"size", len(batch.Requests),
		"key", batch.Key)

	// Attach batch header for the real Azure API.
	header, entries, err := buildBatchHeader(batch)
	if err != nil {
		distributeOperationalError(batch, fmt.Errorf("failed to build header for batch API: %w", err))
		return
	}
	ctxWithHeader := policy.WithHTTPHeader(ctx, http.Header{
		"BatchPutMachine": []string{header},
	})
	// Also mirror entries into context for fakes/testing.
	// See WithFakeBatchEntries for why this duplication is necessary.
	ctxWithHeader = WithFakeBatchEntries(ctxWithHeader, entries)

	// Use resource params from the first request (all requests in a batch
	// share the same resource path due to the key function).
	first := batch.Requests[0].Payload
	template := first.machineBody
	template.Zones = nil
	if template.Properties != nil {
		props := *template.Properties
		clearPerMachineFields(&props)
		template.Properties = &props
	}

	successCount, failCount := 0, 0
	// Note: We discard the SDK poller - callers should use the GET-based poller instead
	_, apiError := e.realClient.BeginCreateOrUpdate(
		ctxWithHeader,
		first.resourceGroupName,
		first.resourceName,
		first.agentPoolName,
		first.machineName,
		*template,
		nil,
	)
	if apiError != nil {
		log.FromContext(ctx).V(2).Info(fmt.Sprintf("AKS machines header batch API call returned error: %s", apiError.Error()),
			"ID", batch.ID)

		// Extract per-machine errors from the parsed API error.
		perMachineErrors := make(map[string]*offerings.HandlableError)
		for _, req := range batch.Requests {
			// Default to no error for each machine.
			// Also, this is to catch the case where API error is erroneously referencing a non-existent machine.
			perMachineErrors[req.Payload.machineName] = nil
		}
		err = extractPerMachineErrors(apiError, perMachineErrors)
		if err != nil {
			distributeOperationalError(batch, fmt.Errorf("failed to extract per-machine errors: %w", err))
			return
		}

		successCount, failCount = distributePerMachine(batch, perMachineErrors)
	} else {
		// All machines in the batch are successfully created (in sync phase)
		successCount = distributeSuccess(batch)
	}

	log.FromContext(ctx).Info("AKS machines header batch execution completed",
		"ID", batch.ID,
		"succeeded", successCount,
		"failed", failCount)
}

// distributePerMachine sends individual API errors back to each request based on the map of machineName → HandlableError.
// Machines with nil HandlableError are treated as successes. Returns the count of successes and failures.
func distributePerMachine(batch *batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError], perMachineErrors map[string]*offerings.HandlableError) (int, int) {
	successCount, failCount := 0, 0
	for _, req := range batch.Requests {
		apiErr := perMachineErrors[req.Payload.machineName]
		req.ResponseChan <- &batcher.Response[*offerings.HandlableError]{Payload: apiErr}
		if apiErr != nil {
			failCount++
		} else {
			successCount++
		}
	}

	return successCount, failCount
}

// distributeSuccess sends a nil-nil response to all requests.
// Returns the count of requests notified.
func distributeSuccess(batch *batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]) int {
	for _, req := range batch.Requests {
		req.ResponseChan <- &batcher.Response[*offerings.HandlableError]{}
	}
	return len(batch.Requests)
}

// distributeOperationalError sends the same operational error (via Err) to all requests.
// Use this only for errors that are not API responses (e.g., header build failure, parse failure).
func distributeOperationalError(batch *batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError], err error) {
	for _, req := range batch.Requests {
		req.ResponseChan <- &batcher.Response[*offerings.HandlableError]{Err: err}
	}
}
