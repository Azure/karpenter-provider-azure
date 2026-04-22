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
func determineBatchKey(item *aksMachineCreatePayload) (string, error) {
	if item == nil || item.machineBody == nil || item.machineBody.Properties == nil {
		return "", fmt.Errorf("nil payload, machine body, or properties")
	}

	template := buildSharedAKSMachineTemplate(*item.machineBody.Properties)
	jsonBytes, err := json.Marshal(template)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(jsonBytes)
	prefix := item.resourceGroupName + "/" + item.resourceName + "/" + item.agentPoolName + "/"

	return prefix + fmt.Sprintf("%x", hash[:8]), nil
}

// buildSharedAKSMachineTemplate returns a Machine containing only the fields shared across all
// machines in a batch, with per-machine and read-only fields zeroed.
// Takes MachineProperties by value so the caller's original is never mutated.
//
// TODO(eidolon): add go doc comment
func buildSharedAKSMachineTemplate(aksMachineProperties armcontainerservice.MachineProperties) armcontainerservice.Machine {
	// Design notes: for each section, we can either:
	// - (A) Recreate a new struct with only the shared fields selected from the old struct, or
	// - (B) Mutate the existing struct to nil-out the per-machine fields
	// (A) risks dropping newly supported fields (by both Karpenter and API) from the request.
	// (B) risks hurting batch performance (size) from not addressing per-machine fields.
	// (B) is chosen as the primary philosophy in MachineProperties. (A) is chosen for top-level Machine struct (unlikely to change too).

	// Clean-up per-machine fields
	// When adding this, also:
	// 1. Add extraction logic in buildBatchHeader (header.go) so the field value is included in the per-machine header entries
	// 2. Add the field to MachineEntry in header.go
	// WARNING: be careful if we want to nil-out a nested field. Merely assigning nil will mutate the caller's struct. Value-copy/deep-copy it first.
	aksMachineProperties.Tags = nil

	// Clean-up read-only fields
	// Technically these won't be populated by default. Though it acts as a safeguard.
	// TODO: consider removing this once acceptance tests for batch are in place?
	aksMachineProperties.ETag = nil
	aksMachineProperties.ProvisioningState = nil
	aksMachineProperties.ResourceID = nil
	aksMachineProperties.Status = nil

	return armcontainerservice.Machine{
		// Dropping all fields outside of properties. They are per-machine/read-only by default.
		Properties: &aksMachineProperties,
	}
}
