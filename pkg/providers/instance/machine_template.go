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

// MachineTemplate provides a type-safe split between shared and per-machine
// configuration for AKS machine creation. This enforces at compile time that
// fields which vary per machine (name, zones, tags) cannot accidentally leak
// into the shared template used for batch grouping.
//
// Design: Proposal B from PR #1455 review discussion.
// See designs/0012-batch-type-safe-config-split.md for full context.
package instance

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

// SharedMachineConfig contains MachineProperties fields that are identical
// across all machines in a batch. The batch grouping hash is computed from
// this struct. Adding a field here automatically includes it in the hash.
type SharedMachineConfig struct {
	Hardware         *armcontainerservice.MachineHardwareProfile
	Kubernetes       *armcontainerservice.MachineKubernetesProfile
	OperatingSystem  *armcontainerservice.MachineOSProfile
	Network          *armcontainerservice.MachineNetworkProperties
	Priority         *armcontainerservice.ScaleSetPriority
	Mode             *armcontainerservice.AgentPoolMode
	NodeImageVersion *string
	Security         *armcontainerservice.MachineSecurityProfile
}

// PerMachineConfig contains fields that vary per machine (per NodeClaim or
// per offering selection). In batch mode, these travel via the BatchPutMachine
// HTTP header instead of the shared request body.
//
// When adding a new per-machine field:
//  1. Add it here
//  2. Update Coordinator.buildBatchHeader to include it in the header entries
//  3. Update MachineEntry in batch/types.go
type PerMachineConfig struct {
	MachineName string
	Zones       []*string
	Tags        map[string]*string
}

// MachineTemplate is the full template returned by buildAKSMachineTemplate.
// It splits configuration into shared (same for all machines in a batch) and
// per-machine (varies per NodeClaim/offering) at the point of creation, making
// the type system enforce the separation.
type MachineTemplate struct {
	Shared     SharedMachineConfig
	PerMachine PerMachineConfig
}

// ToAzureMachine reassembles the split template into an armcontainerservice.Machine
// for use with the Azure SDK (both batch and non-batch code paths).
func (t *MachineTemplate) ToAzureMachine() armcontainerservice.Machine {
	return armcontainerservice.Machine{
		Zones: t.PerMachine.Zones,
		Properties: &armcontainerservice.MachineProperties{
			Hardware:         t.Shared.Hardware,
			Kubernetes:       t.Shared.Kubernetes,
			OperatingSystem:  t.Shared.OperatingSystem,
			Network:          t.Shared.Network,
			Priority:         t.Shared.Priority,
			Mode:             t.Shared.Mode,
			NodeImageVersion: t.Shared.NodeImageVersion,
			Security:         t.Shared.Security,
			Tags:             t.PerMachine.Tags,
		},
	}
}

// SharedProperties returns a MachineProperties containing only shared fields.
// Used by the batch grouper for hashing and by the coordinator for the API body.
func (t *MachineTemplate) SharedProperties() *armcontainerservice.MachineProperties {
	return &armcontainerservice.MachineProperties{
		Hardware:         t.Shared.Hardware,
		Kubernetes:       t.Shared.Kubernetes,
		OperatingSystem:  t.Shared.OperatingSystem,
		Network:          t.Shared.Network,
		Priority:         t.Shared.Priority,
		Mode:             t.Shared.Mode,
		NodeImageVersion: t.Shared.NodeImageVersion,
		Security:         t.Shared.Security,
	}
}
