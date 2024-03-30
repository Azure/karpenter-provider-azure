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
type vnetSubnetResource struct {
	SubscriptionID    string
	ResourceGroupName string
	VNetName          string
	SubnetName        string
}

// GetSubnetResourceID constructs the subnet resource id
func GetSubnetResourceID(subscriptionID, resourceGroupName, virtualNetworkName, subnetName string) string {
	// an example subnet resource: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{virtualNetworkName}/subnets/{subnetName}
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s", subscriptionID, resourceGroupName, virtualNetworkName, subnetName)
}

func GetVnetSubnetIDComponents(vnetSubnetID string) (vnetSubnetResource, error) {
	parts := strings.Split(vnetSubnetID, "/")
	if len(parts) != 11 {
		return vnetSubnetResource{}, fmt.Errorf("invalid vnet subnet id: %s", vnetSubnetID)
	}

	vs := vnetSubnetResource{
		SubscriptionID:    parts[2],
		ResourceGroupName: parts[4],
		VNetName:          parts[8],
		SubnetName:        parts[10],
	}

	//this is a cheap way of ensure all the names match
	mirror := GetSubnetResourceID(vs.SubscriptionID, vs.ResourceGroupName, vs.VNetName, vs.SubnetName)
	if !strings.EqualFold(mirror, vnetSubnetID) {
		return vnetSubnetResource{}, fmt.Errorf("invalid vnet subnet id: %s", vnetSubnetID)
	}
	return vs, nil
}
