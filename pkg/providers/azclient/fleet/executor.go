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

// executor sends batches to the Azure Fleet API.
// It transforms a pending batch into a Fleet CreateOrUpdate call, waits for
// the LRO, runs VM assignment, and distributes results back to each request.
type executor struct {
	fleetClient   FleetAPI
	vmClient      VMAPI
	errorHandler  *offerings.FleetErrorHandler
	clusterName   string
	resourceGroup string
	location      string
}

func newExecutor(
	fleetClient FleetAPI,
	vmClient VMAPI,
	errorHandler *offerings.FleetErrorHandler,
	clusterName, resourceGroup, location string,
) *executor {
	return &executor{
		fleetClient:   fleetClient,
		vmClient:      vmClient,
		errorHandler:  errorHandler,
		clusterName:   clusterName,
		resourceGroup: resourceGroup,
		location:      location,
	}
}

// executeBatch is the batcher.ExecuteBatch[FleetVMProvisionRequest, FleetBatchResponse] implementation.
func (e *executor) executeBatch(ctx context.Context, batch *batcher.Batch[FleetVMProvisionRequest, FleetBatchResponse]) {
	// TODO:
	// 1. Build fleet name: "fleet-{clusterName}-{batchKeyHash8}" via fleetName()
	// 2. Build fleet body via BuildFleetBody()
	// 3. Call e.fleetClient.BeginCreateOrUpdate()
	// 4. Poll LRO to completion
	// 5. List VMs in the fleet (via ARG, filtered by batch-key-hash tag)
	// 6. Call AssignVMsToNodeClaims()
	// 7. Tag assigned VMs via e.vmClient.BeginUpdate() (merge tags)
	// 8. Delete surplus VMs via e.vmClient.BeginDelete()
	// 9. Send FleetBatchResponse to each request's ResponseChan
	// On error: call e.errorHandler.HandleFleetError(), send error to all requests
}

// fleetName returns the deterministic fleet name: "fleet-{clusterName}-{hash8}"
func fleetName(clusterName, batchKeyHash string) string {
	// TODO: return fmt.Sprintf("fleet-%s-%s", clusterName, batchKeyHash)
	return ""
}
