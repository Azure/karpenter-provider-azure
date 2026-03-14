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

package utils

import (
	"fmt"
	"strings"
)

// this parsing function replaces three different functions in different packages that all had bugs. Please don't use a regex to parse these
type VnetSubnetResource struct {
	SubscriptionID    string
	ResourceGroupName string
	VNetName          string
	SubnetName        string
}

func (v VnetSubnetResource) IsSameVNET(cmp VnetSubnetResource) bool {
	if v.SubscriptionID != cmp.SubscriptionID {
		return false
	}
	if v.ResourceGroupName != cmp.ResourceGroupName {
		return false
	}
	if v.VNetName != cmp.VNetName {
		return false
	}
	return true
}

// GetSubnetResourceID constructs the subnet resource id
func GetSubnetResourceID(subscriptionID, resourceGroupName, virtualNetworkName, subnetName string) string {
	// an example subnet resource: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{virtualNetworkName}/subnets/{subnetName}
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s", subscriptionID, resourceGroupName, virtualNetworkName, subnetName)
}

// GetVnetSubnetIDComponents parses an Azure subnet resource ID into its component parts.
// Input: A fully qualified Azure subnet resource ID in the format:
//
//	/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{virtualNetworkName}/subnets/{subnetName}
//
// The input is case-insensitive and must contain exactly 11 slash-separated segments.
// Output: A vnetSubnetResource struct containing:
//   - SubscriptionID: The Azure subscription ID
//   - ResourceGroupName: The resource group name
//   - VNetName: The virtual network name
//   - SubnetName: The subnet name
//
// Returns an error if the input format is invalid or doesn't match the expected structure.
func GetVnetSubnetIDComponents(vnetSubnetID string) (VnetSubnetResource, error) {
	parts := strings.Split(vnetSubnetID, "/")
	if len(parts) != 11 {
		return VnetSubnetResource{}, fmt.Errorf("invalid vnet subnet id: %s", vnetSubnetID)
	}

	vs := VnetSubnetResource{
		SubscriptionID:    parts[2],
		ResourceGroupName: parts[4],
		VNetName:          parts[8],
		SubnetName:        parts[10],
	}

	//this is a cheap way of ensure all the names match
	mirror := GetSubnetResourceID(vs.SubscriptionID, vs.ResourceGroupName, vs.VNetName, vs.SubnetName)
	if !strings.EqualFold(mirror, vnetSubnetID) {
		return VnetSubnetResource{}, fmt.Errorf("invalid vnet subnet id: %s", vnetSubnetID)
	}
	return vs, nil
}
