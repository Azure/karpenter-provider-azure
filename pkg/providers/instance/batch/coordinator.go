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

/*
Coordinator executes batches against Azure. It transforms a PendingBatch into
a single API call using a custom "BatchPutMachine" HTTP header that lists all
machines. This turns N API calls into 1, improving throughput and allowing
Azure to optimize placement.
*/
package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient"
	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Coordinator struct {
	realClient    azclient.AKSMachinesAPI
	resourceGroup string
	clusterName   string
	poolName      string
}

func NewCoordinator(
	realClient azclient.AKSMachinesAPI,
	resourceGroup string,
	clusterName string,
	poolName string,
) *Coordinator {
	return &Coordinator{
		realClient:    realClient,
		resourceGroup: resourceGroup,
		clusterName:   clusterName,
		poolName:      poolName,
	}
}

// ExecuteBatch sends a batch to Azure as one API call, then distributes
// results (success or per-machine errors) back to each request's channel.
//
// Uses context.Background() intentionally: a batch serves multiple callers with
// different deadlines, and canceling an in-flight PUT mid-request risks creating
// phantom Azure resources that Karpenter doesn't track. The callers' own contexts
// protect them from indefinite waits on their response channels.
func (c *Coordinator) ExecuteBatch(batch *PendingBatch) {
	ctx := context.Background()
	batchID := uuid.New().String()

	log.FromContext(ctx).Info("executing batch",
		"batchID", batchID,
		"size", len(batch.requests),
		"templateHash", batch.templateHash)

	header, entries, err := c.buildBatchHeader(batch)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build batch header")
		c.distributeError(batch, err)
		return
	}

	// Attach batch header for the real Azure API.
	ctxWithHeader := policy.WithHTTPHeader(ctx, http.Header{
		"BatchPutMachine": []string{header},
	})
	// Also mirror entries into context for fakes/testing.
	// See WithFakeBatchEntries for why this duplication is necessary.
	ctxWithHeader = WithFakeBatchEntries(ctxWithHeader, entries)

	// Build the PUT body from the primary machine's template.
	//
	// The primary machine (batch.requests[0]) keeps its per-machine fields
	// (tags, zones) in the body — the RP reads them normally from the body.
	// Non-primary machines get their per-machine fields from the BatchPutMachine
	// header instead. This matches how the RP's PrepareBatchMachines works:
	// it uses the body for the primary and the header for clones.
	//
	// We still need to clear per-machine fields from the *shared* properties
	// that the RP will clone for non-primary machines. However, the RP
	// overwrites cloned machines' tags (line 112) and zones (lines 114-118)
	// from the header, so it's safe to leave the primary's values in the body.
	template := batch.requests[0].template

	poller, err := c.realClient.BeginCreateOrUpdate(
		ctxWithHeader,
		c.resourceGroup,
		c.clusterName,
		c.poolName,
		batch.requests[0].machineName, // First machine is the "primary"
		template,
		nil,
	)

	// Distribute results to each caller
	// Note: We discard the SDK poller - callers should use the GET-based poller instead
	_ = poller

	// If there's an API-level error, try to parse per-machine errors from it
	frontendErrors := c.parseFrontendErrors(err)

	// If there's an API-level error but no per-machine breakdown, all machines failed
	if err != nil && frontendErrors == nil {
		log.FromContext(ctx).Error(err, "batch API call failed, distributing error to all machines",
			"batchID", batchID,
			"size", len(batch.requests))
		c.distributeError(batch, err)
		return
	}

	successCount, failCount := 0, 0

	for _, req := range batch.requests {
		if machineErr, hasFailed := frontendErrors[req.machineName]; hasFailed {
			req.responseChan <- &CreateResponse{Poller: nil, Err: machineErr, BatchID: batchID}
			failCount++
		} else {
			// Note: We don't propagate batch metadata via req.ctx here because
			// the caller already received its response through the channel and
			// never reads req.ctx again. Batch identification is via the BatchID
			// field in CreateResponse.
			req.responseChan <- &CreateResponse{Poller: nil, Err: nil, BatchID: batchID}
			successCount++
		}
	}

	log.FromContext(ctx).Info("batch completed",
		"batchID", batchID,
		"succeeded", successCount,
		"failed", failCount)
}

// buildBatchHeader creates the JSON for the BatchPutMachine HTTP header
// and returns the per-machine entries for context propagation.
//
// The primary machine (batch.requests[0]) is EXCLUDED from batchMachines.
// Its per-machine fields (tags, zones) travel in the PUT body instead.
// The RP's PrepareBatchMachines creates the primary from the body and clones
// it for each batchMachines entry — it does NOT apply header fields to the
// primary. If the primary were included in the header, its entry would be
// silently discarded by cleanUpInvalidDuplicateMachinesAndZones (which
// pre-seeds "seen" with the PUT URL machine name), and the primary would
// end up with no tags and no zone.
func (c *Coordinator) buildBatchHeader(batch *PendingBatch) (string, []MachineEntry, error) {
	// Start from index 1: the primary (index 0) gets its fields from the body.
	entries := make([]MachineEntry, 0, len(batch.requests)-1)
	for i, req := range batch.requests {
		if i == 0 {
			continue // Primary machine — fields travel in the PUT body
		}
		var tags map[string]string
		if req.template.Properties != nil {
			tags = extractTags(req.template.Properties.Tags)
		} else {
			tags = make(map[string]string)
		}
		entries = append(entries, MachineEntry{
			MachineName: req.machineName,
			Zones:       extractZones(req.template.Zones),
			Tags:        tags,
		})
	}

	header := BatchPutMachineHeader{
		VMSkus:        VMSkus{Value: []interface{}{}},
		BatchMachines: entries,
	}

	jsonBytes, err := json.Marshal(header)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal batch header: %w", err)
	}
	return string(jsonBytes), entries, nil
}

// parseFrontendErrors extracts per-machine errors from Azure's response.
// TODO: Implement actual parsing of Azure's structured error response.
// Currently returns nil to signal "no per-machine breakdown available",
// which causes the caller to treat all machines as failed uniformly.
//
// IMPORTANT for future implementors: When implementing this, the return
// contract must be that machines NOT present in the returned map are
// treated as failed (not succeeded), unless the overall err is nil.
// This is because a partial parse that misses some machines should fail
// safe rather than report phantom successes.
func (c *Coordinator) parseFrontendErrors(err error) map[string]error { //nolint:unparam // stub — result will be non-nil once Azure error parsing is implemented
	if err == nil {
		return nil
	}
	// TODO: Parse Azure's structured error response to extract per-machine failures.
	// Return nil to indicate "could not parse" → all machines will be failed uniformly.
	return nil
}

// distributeError sends the same error to all requests (used for early failures).
func (c *Coordinator) distributeError(batch *PendingBatch, err error) {
	for _, req := range batch.requests {
		req.responseChan <- &CreateResponse{Poller: nil, Err: err}
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
