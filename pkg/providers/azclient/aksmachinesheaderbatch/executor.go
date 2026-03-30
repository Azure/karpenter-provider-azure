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
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// aksMachineCreatePayload is the per-request payload to be batched on.
type aksMachineCreatePayload struct {
	resourceGroupName string
	resourceName      string
	agentPoolName     string
	machineName       string
	machineBody       armcontainerservice.Machine
}

// executor sends batches to Azure using the BatchPutMachine HTTP header.
// It transforms a pending batch into a single API call, then distributes
// per-machine results back to each request's channel.
type executor struct {
	realClient azapi.AKSMachinesAPI
}

func newExecutor(realClient azapi.AKSMachinesAPI) *executor {
	return &executor{realClient: realClient}
}

// executeBatch is the batcher.ExecuteBatch — it sends a batch to Azure as one
// API call, then distributes results back to each request's channel.
//
// Uses context.Background() intentionally: a batch serves multiple callers with
// different deadlines, and canceling an in-flight PUT mid-request risks creating
// phantom Azure resources that Karpenter doesn't track.
//
//nolint:govet // nilness: frontendErrors is intentionally always nil until per-machine error parsing TODO is implemented
func (e *executor) executeBatch(batch *batcher.Batch[aksMachineCreatePayload, struct{}]) {
	ctx := context.Background()
	batchID := uuid.New().String()

	log.FromContext(ctx).Info("executing batch",
		"batchID", batchID,
		"size", len(batch.Requests),
		"key", batch.Key)

	// Attach batch header for the real Azure API.
	header, entries, err := buildBatchHeader(batch)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build batch header")
		distributeError(batch, err)
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

	// Note: We discard the SDK poller - callers should use the GET-based poller instead
	_, err = e.realClient.BeginCreateOrUpdate(
		ctxWithHeader,
		first.resourceGroupName,
		first.resourceName,
		first.agentPoolName,
		first.machineName,
		template,
		nil,
	)

	// If there's an API-level error, try to parse per-machine errors from it
	// TODO: Implement actual parsing of Azure's structured error response.
	// frontendErrors := e.parseFrontendErrors(err)
	var frontendErrors map[string]error // Placeholder, as if all machines failed.

	// If there's an API-level error but no per-machine breakdown, all machines failed
	if err != nil && frontendErrors == nil {
		log.FromContext(ctx).Error(err, "batch API call failed, distributing error to all machines",
			"batchID", batchID,
			"size", len(batch.Requests))
		distributeError(batch, err)
		return
	}

	successCount, failCount := 0, 0

	for _, req := range batch.Requests {
		if machineErr, hasFailed := frontendErrors[req.Payload.machineName]; hasFailed {
			req.ResponseChan <- &batcher.Response[struct{}]{Err: machineErr}
			failCount++
		} else {
			req.ResponseChan <- &batcher.Response[struct{}]{Err: nil}
			successCount++
		}
	}

	log.FromContext(ctx).Info("batch completed",
		"batchID", batchID,
		"succeeded", successCount,
		"failed", failCount)
}

// distributeError sends the same error to all requests (used for early failures).
func distributeError(batch *batcher.Batch[aksMachineCreatePayload, struct{}], err error) {
	for _, req := range batch.Requests {
		req.ResponseChan <- &batcher.Response[struct{}]{Err: err}
	}
}

// Helpers to convert Azure SDK pointer types to concrete values.

func extractZones(zones []*string) []string {
	if len(zones) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(zones))
	for _, z := range zones {
		if z != nil {
			result = append(result, *z)
		}
	}
	return result
}

func extractTags(tags map[string]*string) map[string]string {
	if tags == nil {
		return make(map[string]string)
	}
	result := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			result[k] = *v
		}
	}
	return result
}
