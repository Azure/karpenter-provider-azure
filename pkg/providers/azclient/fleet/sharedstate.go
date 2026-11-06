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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
)

// FleetBatchResponse is the per-request response distributed by the executor.
// Exactly one of SharedState or Error is set.
type FleetBatchResponse struct {
	SharedState *FleetSharedState
	Error       error
}

// FleetSharedState holds the outcome of one Fleet create, shared by every request in the
// (sub-)batch. It is fully populated before distribution and is read-only afterwards, so
// concurrent readers need no synchronization.
type FleetSharedState struct {
	fleetName   string
	assignments map[string]*FleetAssignment
	surplus     []*armcompute.VirtualMachine
	err         error
}

// newFleetSharedState assigns the listed VMs to the batch's NodeClaims.
func newFleetSharedState(fleetName string, nodeClaimNames []string, vms []*armcompute.VirtualMachine) *FleetSharedState {
	state := &FleetSharedState{fleetName: fleetName}
	if len(vms) == 0 && len(nodeClaimNames) > 0 {
		state.err = fmt.Errorf("fleet %s created no VMs", fleetName)
		return state
	}
	state.assignments, _, state.surplus = AssignVMsToNodeClaims(nodeClaimNames, vms)
	return state
}

// FleetName returns the Fleet resource name that created the VMs.
func (s *FleetSharedState) FleetName() string {
	return s.fleetName
}

// GetAssignment returns the NodeClaim's VM, or nil when Fleet created too few VMs.
func (s *FleetSharedState) GetAssignment(nodeClaimName string) *FleetAssignment {
	return s.assignments[nodeClaimName]
}

// GetError returns the batch-wide error, or nil when the Fleet produced VMs.
func (s *FleetSharedState) GetError() error {
	return s.err
}

// Surplus returns VMs that were not assigned to any NodeClaim.
func (s *FleetSharedState) Surplus() []*armcompute.VirtualMachine {
	return s.surplus
}
