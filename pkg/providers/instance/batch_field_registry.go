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

// This file is the central registry for fields that receive special treatment
// during batch VM creation. It lives in the instance package (next to
// buildAKSMachineTemplate) so that developers modifying the template builder
// naturally encounter it when adding or changing per-NodeClaim fields.
//
// The batch system (pkg/providers/instance/batch/) imports these functions to
// decide which fields to exclude from the grouping hash and which to strip from
// the shared request body.
//
// ## When to update this file
//
// Update ClearPerMachineFields if you add a MachineProperties field whose value
// varies per NodeClaim (like Tags, which carry the NodeClaim name). Fields that
// are the same for all NodeClaims sharing a NodePool+NodeClass (like VMSize,
// Priority, KubeletConfig) should NOT be added here — they are part of the
// shared template and must influence the batch grouping hash.
//
// Update ClearReadOnlyFields if the Azure SDK adds a new server-set field to
// MachineProperties that should never influence hashing or request bodies.
//
// ## Design context (PR #1455 review discussion)
//
// This registry was introduced to solve a developer-experience gap: when someone
// modifies buildAKSMachineTemplate() to set a new per-NodeClaim field, they need
// to also exclude it from the batch grouping hash. Without this file in the same
// package, that requirement was invisible — the batch module is a subpackage that
// template-builder developers may never look at.
//
// The reflection-based guardrail test (TestComputeTemplateHash_AllFieldsAccountedFor
// in batch/grouper_test.go) catches new SDK fields at test time. This registry
// catches new per-NodeClaim field wiring at development time by being co-located
// with the template builder.
//
// ## Known gap: offerings-related fields
//
// Some fields like Machine.Zones are selected from instance type offerings within
// the instance package, not from NodeClaim-unique data. They are per-machine but
// not per-NodeClaim — they come from the scheduler's zone selection. These live on
// the Machine struct (not MachineProperties), so they don't go through this
// registry. If the Machine API adds new offerings-related fields to
// MachineProperties in the future, this distinction will need to be revisited.
//
// ## Relationship with MachineTemplate (Proposal B)
//
// The type-safe config split (machine_template.go) enforces the shared vs
// per-machine classification at compile time in buildAKSMachineTemplate().
// This registry remains necessary because the batch system receives a flat
// armcontainerservice.Machine through the AKSMachinesAPI interface boundary,
// so it still needs runtime field-clearing for hashing and body construction.
// The two mechanisms are complementary: MachineTemplate catches misclassification
// at the creation point, and this registry catches it within the batch internals.
// See designs/0012-batch-type-safe-config-split.md.
package instance

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

// ClearPerMachineFields zeros MachineProperties fields that vary per machine and
// travel via the BatchPutMachine HTTP header instead of the shared request body.
//
// This is the single source of truth for per-machine MachineProperties fields.
// Both the batch grouper (computeTemplateHash) and coordinator (ExecuteBatch)
// call this function, so adding a field here automatically:
//   - Excludes it from the batch grouping hash (machines differing only on this
//     field will batch together)
//   - Strips it from the shared API request body (the field travels in the
//     BatchPutMachine header instead)
//
// When adding a field here, also:
//  1. Update the excludedFields map in TestComputeTemplateHash_AllFieldsAccountedFor
//     (batch/grouper_test.go)
//  2. Add extraction logic in Coordinator.buildBatchHeader (batch/coordinator.go)
//     so the field value is included in the per-machine header entries
//  3. Add the field to MachineEntry in batch/types.go
func ClearPerMachineFields(props *armcontainerservice.MachineProperties) {
	props.Tags = nil
}

// ClearReadOnlyFields zeros MachineProperties fields that are set by the server
// and should never influence template hashing or request bodies.
//
// These fields appear on MachineProperties in GET responses but are not part of
// the creation template. Adding a new server-set field here also requires updating
// the excludedFields map in TestComputeTemplateHash_AllFieldsAccountedFor.
func ClearReadOnlyFields(props *armcontainerservice.MachineProperties) {
	props.ETag = nil
	props.ProvisioningState = nil
	props.ResourceID = nil
	props.Status = nil
}
