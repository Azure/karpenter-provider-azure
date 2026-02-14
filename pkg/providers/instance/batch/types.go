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
Data types for the batch system.

Flow: CreateRequest → PendingBatch → BatchPutMachineHeader → CreateResponse
*/
package batch

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

// CreateRequest is a single VM creation request. The caller waits on responseChan.
type CreateRequest struct {
	ctx           context.Context
	resourceGroup string
	clusterName   string
	poolName      string
	machineName   string
	template      armcontainerservice.Machine
	responseChan  chan *CreateResponse
}

// CreateResponse is sent back via the request's channel after batch execution.
// Contains either a Poller (success) or Err (failure).
type CreateResponse struct {
	Poller  *runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse]
	Err     error
	BatchID string // For log correlation
}

// PendingBatch groups requests with the same template hash.
type PendingBatch struct {
	templateHash string
	template     armcontainerservice.Machine
	requests     []*CreateRequest
}

// BatchPutMachineHeader is the JSON structure sent via HTTP header to Azure.
// It lists per-machine variations (name, zones, tags) while the API body
// contains the shared template.
type BatchPutMachineHeader struct {
	VMSkus        VMSkus         `json:"vmSkus"`
	BatchMachines []MachineEntry `json:"batchMachines"`
}

type VMSkus struct {
	Value []interface{} `json:"value"`
}

type MachineEntry struct {
	MachineName string            `json:"machineName"`
	Zones       []string          `json:"zones"`
	Tags        map[string]string `json:"tags"`
}
