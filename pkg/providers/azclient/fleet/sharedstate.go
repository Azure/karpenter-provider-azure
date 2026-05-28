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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// NewFleetSharedState creates a shared state for a batch. Called by the executor
// after submitting the Fleet LRO. The VMs field is populated by the executor
// after listing Fleet VMs (via ARG or VMSS list).
func NewFleetSharedState(
	requests []*VMAssignmentRequest,
	instanceTypes map[string]*cloudprovider.InstanceType,
	vmClient VMAPI,
	fleetName, resourceGroup string,
) *FleetSharedState {
	return &FleetSharedState{
		requests:      requests,
		instanceTypes: instanceTypes,
		vmClient:      vmClient,
		fleetName:     fleetName,
		resourceGroup: resourceGroup,
	}
}

// NewFleetSharedStateForTest creates a shared state with pre-resolved VMs (no LRO).
// Used in unit tests to bypass the poller and directly test the assign→tag→delete flow.
func NewFleetSharedStateForTest(
	vms []*armcompute.VirtualMachine,
	requests []*VMAssignmentRequest,
	instanceTypes map[string]*cloudprovider.InstanceType,
	vmClient VMAPI,
	fleetName, resourceGroup string,
) *FleetSharedState {
	return &FleetSharedState{
		injectedVMs:   vms,
		requests:      requests,
		instanceTypes: instanceTypes,
		vmClient:      vmClient,
		fleetName:     fleetName,
		resourceGroup: resourceGroup,
	}
}

// SetVMs allows the executor to inject listed VMs before promises call Wait().
// This is the production path (vs injectedVMs set at construction for tests).
func (s *FleetSharedState) SetVMs(vms []*armcompute.VirtualMachine) {
	s.injectedVMs = vms
}

// SetError allows the executor to set a batch-wide error (e.g., LRO failure).
func (s *FleetSharedState) SetError(err error) {
	s.err = err
}

// ExecuteSharedPoll runs the assignment logic exactly once (via sync.Once).
// Multiple concurrent callers (from different promise Wait() calls) will block
// until the first caller completes.
func (s *FleetSharedState) ExecuteSharedPoll(ctx context.Context) {
	s.once.Do(func() {
		s.executePoll(ctx)
	})
}

// executePoll performs the actual assignment work:
// 1. Use injected VMs (executor already polled LRO and listed VMs)
// 2. Run assignment matching
// 3. Tag assigned VMs
// 4. Delete surplus VMs (best-effort)
func (s *FleetSharedState) executePoll(ctx context.Context) {
	// If executor already set an error (LRO failure), short-circuit.
	if s.err != nil {
		return
	}

	vms := s.injectedVMs
	if len(vms) == 0 && len(s.requests) > 0 {
		s.err = fmt.Errorf("no VMs available for fleet %s", s.fleetName)
		return
	}

	// Run assignment.
	assigned, _, surplus := AssignVMsToNodeClaims(s.requests, vms, s.instanceTypes)
	s.assignments = assigned
	s.surplus = surplus

	// Tag assigned VMs with per-NodeClaim identity (best-effort).
	s.tagAssignedVMs(ctx, assigned)

	// Delete surplus VMs (best-effort, don't fail the batch).
	s.deleteSurplusVMs(ctx, surplus)
}

// tagAssignedVMs patches each assigned VM with the nodeclaim-name tag.
// Tag failure is non-fatal: the VM is still delivered to the promise.
// We merge the new tag into the VM's existing tags to avoid replacing them.
func (s *FleetSharedState) tagAssignedVMs(ctx context.Context, assignments map[string]*FleetAssignment) {
	if s.vmClient == nil {
		return
	}
	for ncName, a := range assignments {
		if a == nil || a.VM == nil || a.VM.Name == nil {
			continue
		}
		// Merge: start with existing VM tags (inherited from Fleet), then add nodeclaim tag.
		mergedTags := make(map[string]*string, len(a.VM.Tags)+1)
		for k, v := range a.VM.Tags {
			mergedTags[k] = v
		}
		mergedTags["karpenter.azure.com_nodeclaim-name"] = lo.ToPtr(ncName)

		update := armcompute.VirtualMachineUpdate{Tags: mergedTags}
		poller, err := s.vmClient.BeginUpdate(ctx, s.resourceGroup, *a.VM.Name, update, nil)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to tag VM", "vm", *a.VM.Name, "nodeclaim", ncName)
			continue
		}
		if poller == nil {
			continue
		}
		// Fire-and-forget poll for POC; real implementation may batch these.
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			log.FromContext(ctx).Error(err, "tag poll failed", "vm", *a.VM.Name, "nodeclaim", ncName)
		}
	}
}

// deleteSurplusVMs attempts to delete VMs that weren't matched to any request.
// Best-effort: failures are logged but don't affect the batch result.
func (s *FleetSharedState) deleteSurplusVMs(ctx context.Context, surplus []*armcompute.VirtualMachine) {
	if s.vmClient == nil {
		return
	}
	for _, vm := range surplus {
		if vm == nil || vm.Name == nil {
			continue
		}
		_, err := s.vmClient.BeginDelete(ctx, s.resourceGroup, *vm.Name, nil)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to delete surplus VM", "vm", *vm.Name)
		}
	}
}

// GetAssignment returns the assignment for a NodeClaim, or nil if unmatched.
func (s *FleetSharedState) GetAssignment(nodeClaimName string) *FleetAssignment {
	if s.assignments == nil {
		return nil
	}
	return s.assignments[nodeClaimName]
}

// GetError returns the batch-wide error, or nil if the poll succeeded.
func (s *FleetSharedState) GetError() error {
	return s.err
}

// GetVMClient returns the VM API client for cleanup operations.
func (s *FleetSharedState) GetVMClient() VMAPI {
	return s.vmClient
}

// GetResourceGroup returns the resource group name.
func (s *FleetSharedState) GetResourceGroup() string {
	return s.resourceGroup
}
