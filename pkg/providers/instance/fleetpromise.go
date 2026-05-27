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

package instance

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/fleet"
)

// FleetMemberPromise implements the Promise interface for Fleet-provisioned VMs.
// Unlike VirtualMachinePromise, VM identity is unknown at construction time —
// fields are populated lazily inside Wait() after the batch completes.
type FleetMemberPromise struct {
	sharedState   *fleet.FleetSharedState
	nodeClaimName string
	capacityType  string
	fleetName     string

	// Populated after Wait() completes successfully
	VM           *armcompute.VirtualMachine
	InstanceType *cloudprovider.InstanceType
	Zone         string
	ProviderID   string
}

// Ensure FleetMemberPromise implements Promise.
var _ Promise = (*FleetMemberPromise)(nil)

// Wait blocks until the fleet batch completes and a VM is assigned to this NodeClaim.
// Returns InsufficientCapacityError if no VM was assigned.
func (p *FleetMemberPromise) Wait() error {
	// TODO(fleet-poc-mh-cloudprovider-create): pass real ctx from caller
	p.sharedState.ExecuteSharedPoll(context.TODO())

	if err := p.sharedState.GetError(); err != nil {
		return err
	}

	assignment := p.sharedState.GetAssignment(p.nodeClaimName)
	if assignment == nil {
		return cloudprovider.NewInsufficientCapacityError(
			fmt.Errorf("no VM assigned for NodeClaim %s in fleet %s", p.nodeClaimName, p.fleetName))
	}

	p.VM = assignment.VM
	p.InstanceType = assignment.InstanceType
	p.Zone = assignment.Zone
	if p.VM != nil && p.VM.ID != nil {
		p.ProviderID = *p.VM.ID
	}
	return nil
}

// Cleanup deletes the assigned VM if one exists. No-op if Wait() wasn't called or no VM was assigned.
func (p *FleetMemberPromise) Cleanup(ctx context.Context) error {
	if p.VM == nil || p.VM.Name == nil {
		return nil
	}
	vmClient := p.sharedState.GetVMClient()
	if vmClient == nil {
		return nil
	}
	poller, err := vmClient.BeginDelete(ctx, p.sharedState.GetResourceGroup(), *p.VM.Name, nil)
	if err != nil {
		return fmt.Errorf("cleanup VM %s: %w", *p.VM.Name, err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// GetInstanceName returns the assigned VM name, or empty string if not yet assigned.
func (p *FleetMemberPromise) GetInstanceName() string {
	if p.VM != nil && p.VM.Name != nil {
		return *p.VM.Name
	}
	return ""
}
