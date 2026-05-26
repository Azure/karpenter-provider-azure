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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

// FleetAssignment represents a VM assigned to a specific NodeClaim.
type FleetAssignment struct {
	VM           *armcompute.VirtualMachine
	InstanceType *cloudprovider.InstanceType
	Zone         string
}

// VMAssignmentRequest is the per-NodeClaim entry used during the assignment phase.
// It describes what SKUs/zones a NodeClaim can accept and where to send the result.
type VMAssignmentRequest struct {
	NodeClaimName   string
	AcceptableSKUs  []string
	AcceptableZones []string
	InstanceTypes   map[string]*cloudprovider.InstanceType
	ResponseChan    chan *batcher.Response[FleetBatchResponse]
}

// AssignVMsToNodeClaims matches Fleet-created VMs to NodeClaim requests by SKU×zone.
// Returns:
//   - assigned: map[nodeClaimName]*FleetAssignment for matched requests
//   - unmatched: requests that could not be satisfied
//   - surplus: VMs that were not matched to any request
func AssignVMsToNodeClaims(
	requests []*VMAssignmentRequest,
	vms []*armcompute.VirtualMachine,
	instanceTypes map[string]*cloudprovider.InstanceType,
) (assigned map[string]*FleetAssignment, unmatched []*VMAssignmentRequest, surplus []*armcompute.VirtualMachine) {
	// TODO:
	// 1. Build SKU×zone buckets from VMs
	// 2. For each request (FIFO order), find first matching VM by SKU+zone intersection
	// 3. Pop from bucket, create assignment
	// 4. Remaining VMs in buckets = surplus
	return nil, nil, nil
}
