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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"
)

// BuildFleetBody constructs the armcomputefleet.Fleet resource body for a CreateOrUpdate call.
// Handles spot vs regular profile, VMSS Flex model, network profile, tags.
func BuildFleetBody(
	fleetName string,
	fields BatchKeyFields,
	vmSizes []string,
	targetCapacity int,
	capacityType string,
	tags map[string]*string,
	spotMaxPrice *float32,
	location string,
	nodeIdentities []string,
	lbBackendPools []string,
) *armcomputefleet.Fleet {
	// TODO: build Fleet body with:
	//   - SpotPriorityProfile (maintain:false) OR RegularPriorityProfile
	//   - VMSSFlex compute profile with vmSizes
	//   - Network profile from subnetID/NSG
	//   - OS profile, storage profile from fields
	//   - Tags: karpenter.azure.com_managed-by=karpenter, batch-key-hash=<hash>
	return nil
}

// buildNetworkProfile constructs the VMSS network profile with subnet, NSG, and LB pools.
func buildNetworkProfile(subnetID, nsgID string, lbBackendPools []string) *armcomputefleet.VirtualMachineScaleSetNetworkProfile {
	// TODO: build NIC config with subnet, NSG, accelerated networking, LB backend pools
	return nil
}
