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
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
)

// determineBatchKey computes a grouping key from the AKS machine to be created.
// Requests with the same resource path and AKS machine properties (excluding per-machine fields like Tags and Zones) batch together.
func determineBatchKey(item *aksMachineCreatePayload) string {
	// Include resource path so requests to different clusters/pools don't mix.
	prefix := item.resourceGroupName + "/" + item.resourceName + "/" + item.agentPoolName + "/"

	if item.machineBody.Properties == nil {
		return prefix
	}
	props := *item.machineBody.Properties
	clearPerMachineFields(&props)
	clearReadOnlyFields(&props)

	jsonBytes, err := json.Marshal(props)
	if err != nil {
		jsonBytes = []byte(fmt.Sprintf("%+v", props))
	}
	hash := sha256.Sum256(jsonBytes)
	return prefix + fmt.Sprintf("%x", hash[:8])
}

// clearPerMachineFields zeros MachineProperties fields that vary per machine and
// travel via the BatchPutMachine HTTP header instead of the shared request body.
//
// When adding a field here, also (at least):
//  1. Add extraction logic in executor.buildBatchHeader (executor.go)
//     so the field value is included in the per-machine header entries
//  2. Add the field to MachineEntry in types.go
func clearPerMachineFields(props *armcontainerservice.MachineProperties) {
	props.Tags = nil
	// Zones field is not in Properties (but in its parent--Machine struct).
}

// clearReadOnlyFields zeros MachineProperties fields that are set by the server
// and should never influence template hashing or request bodies.
// It is unlikely these fields would be set on a create request in the first place, but just in case.
func clearReadOnlyFields(props *armcontainerservice.MachineProperties) {
	props.ETag = nil
	props.ProvisioningState = nil
	props.ResourceID = nil
	props.Status = nil
}
