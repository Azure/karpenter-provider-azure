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
	"github.com/samber/lo"
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

// skuZoneKey is the bucket key for assignment: a (SKU, Zone) pair.
type skuZoneKey struct {
	SKU  string
	Zone string
}

// AssignVMsToNodeClaims matches Fleet-created VMs to NodeClaim requests by SKU×zone
// in FIFO order over each request's cross-product of AcceptableSKUs × AcceptableZones.
//
// The function is pure: it does not modify the input slices and has no external side effects.
//
// Returns:
//   - assigned: nodeClaimName → FleetAssignment for every request that found a VM
//   - unmatched: requests that could not be satisfied by any (sku, zone) bucket
//   - surplus: VMs in arbitrary order that were not claimed by any request
//
// The instanceTypes parameter is currently unused; per-request InstanceTypes drive
// the lookup. TODO(fleet-poc-mh-executor): drop this parameter once the executor wires up.
func AssignVMsToNodeClaims(
	requests []*VMAssignmentRequest,
	vms []*armcompute.VirtualMachine,
	_ map[string]*cloudprovider.InstanceType,
) (assigned map[string]*FleetAssignment, unmatched []*VMAssignmentRequest, surplus []*armcompute.VirtualMachine) {
	if len(requests) == 0 && len(vms) == 0 {
		return nil, nil, nil
	}

	// 1. Build SKU×Zone buckets from VMs.
	buckets := make(map[skuZoneKey][]*armcompute.VirtualMachine, len(vms))
	for _, vm := range vms {
		sku, zone, ok := skuAndZone(vm)
		if !ok {
			// Malformed VM — treat as surplus so the caller can decide to log/delete.
			surplus = append(surplus, vm)
			continue
		}
		key := skuZoneKey{SKU: sku, Zone: zone}
		buckets[key] = append(buckets[key], vm)
	}

	// 2. Match requests in FIFO order.
	assigned = make(map[string]*FleetAssignment, len(requests))
	for _, req := range requests {
		if req == nil {
			continue
		}
		if vm, sku, zone, ok := popMatch(buckets, req); ok {
			assigned[req.NodeClaimName] = &FleetAssignment{
				VM:           vm,
				InstanceType: req.InstanceTypes[sku],
				Zone:         zone,
			}
			continue
		}
		unmatched = append(unmatched, req)
	}

	// 3. Remaining VMs in buckets are surplus.
	for _, remaining := range buckets {
		surplus = append(surplus, remaining...)
	}
	return assigned, unmatched, surplus
}

// popMatch walks the cross-product of req.AcceptableSKUs × req.AcceptableZones in slice
// order and returns the first available VM, removing it from the bucket. Returns ok=false
// if no combination matches.
func popMatch(buckets map[skuZoneKey][]*armcompute.VirtualMachine, req *VMAssignmentRequest) (
	*armcompute.VirtualMachine, string, string, bool,
) {
	for _, sku := range req.AcceptableSKUs {
		for _, zone := range req.AcceptableZones {
			key := skuZoneKey{SKU: sku, Zone: zone}
			bucket := buckets[key]
			if len(bucket) == 0 {
				continue
			}
			vm := bucket[0]
			buckets[key] = bucket[1:]
			return vm, sku, zone, true
		}
	}
	return nil, "", "", false
}

// skuAndZone extracts the SKU string and zone from a Fleet-created VM.
// Returns ok=false if any required field is missing.
func skuAndZone(vm *armcompute.VirtualMachine) (string, string, bool) {
	if vm == nil || vm.Properties == nil ||
		vm.Properties.HardwareProfile == nil ||
		vm.Properties.HardwareProfile.VMSize == nil {
		return "", "", false
	}
	sku := string(*vm.Properties.HardwareProfile.VMSize)
	zone := ""
	if len(vm.Zones) > 0 {
		zone = lo.FromPtr(vm.Zones[0])
	}
	return sku, zone, true
}
