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
func (c *Coordinator) ExecuteBatch(batch *PendingBatch) error {
	ctx := context.Background()
	batchID := uuid.New().String()

	log.FromContext(ctx).Info("executing batch",
		"batchID", batchID,
		"size", len(batch.requests),
		"templateHash", batch.templateHash)

	header, err := c.buildBatchHeader(batch)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to build batch header")
		c.distributeError(batch, err)
		return err
	}

	// Attach batch header and make the API call
	ctxWithHeader := policy.WithHTTPHeader(ctx, http.Header{
		"BatchPutMachine": []string{header},
	})

	poller, err := c.realClient.BeginCreateOrUpdate(
		ctxWithHeader,
		c.resourceGroup,
		c.clusterName,
		c.poolName,
		batch.requests[0].machineName, // First machine is the "primary"
		batch.template,
		nil,
	)

	// Distribute results to each caller
	// Note: We discard the SDK poller - callers should use the GET-based poller instead
	_ = poller

	// If there's an API-level error, try to parse per-machine errors from it
	frontendErrors := c.parseFrontendErrors(err)

	// If we have an error but couldn't parse per-machine errors, all machines failed
	if err != nil && len(frontendErrors) == 0 {
		log.FromContext(ctx).Error(err, "batch API call failed, distributing error to all machines",
			"batchID", batchID,
			"size", len(batch.requests))
		c.distributeError(batch, err)
		return err
	}

	successCount, failCount := 0, 0

	for _, req := range batch.requests {
		if machineErr, hasFailed := frontendErrors[req.machineName]; hasFailed {
			req.responseChan <- &CreateResponse{Poller: nil, Err: machineErr, BatchID: batchID}
			failCount++
		} else {
			req.ctx = WithBatchMetadata(req.ctx, &BatchMetadata{
				IsBatched:   true,
				MachineName: req.machineName,
				BatchID:     batchID,
			})
			// Return nil poller - caller will use GET-based polling
			req.responseChan <- &CreateResponse{Poller: nil, Err: nil, BatchID: batchID}
			successCount++
		}
	}

	log.FromContext(ctx).Info("batch completed",
		"batchID", batchID,
		"succeeded", successCount,
		"failed", failCount)

	return nil
}

// buildBatchHeader creates the JSON for the BatchPutMachine HTTP header.
func (c *Coordinator) buildBatchHeader(batch *PendingBatch) (string, error) {
	header := BatchPutMachineHeader{
		VMSkus:        VMSkus{Value: []interface{}{}},
		BatchMachines: make([]MachineEntry, 0, len(batch.requests)),
	}

	for _, req := range batch.requests {
		header.BatchMachines = append(header.BatchMachines, MachineEntry{
			MachineName: req.machineName,
			Zones:       extractZones(req.template.Zones),
			Tags:        extractTags(req.template.Properties.Tags),
		})
	}

	jsonBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal batch header: %w", err)
	}
	return string(jsonBytes), nil
}

// parseFrontendErrors extracts per-machine errors from Azure's response.
// TODO: Implement actual parsing. Currently treats all machines uniformly.
func (c *Coordinator) parseFrontendErrors(err error) map[string]error {
	errors := make(map[string]error)
	if err == nil {
		return errors
	}
	// TODO: Parse Azure's structured error response
	return errors
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
