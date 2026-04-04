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

package zones

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
)

// Regional is the zone label value AKS assigns to regional (non-zonal) VMs.
// This matches the topology.kubernetes.io/zone label on regional AKS nodes,
// Technically this value represents fault domain, but for standalone VMs this is always "0";
// see https://github.com/kubernetes-sigs/cloud-provider-azure/blob/84bacac916c52e3dbae8ec01f1d8ab5a20267b7d/pkg/provider/azure_standard.go#L579-L601
const Regional = "0"

// MakeAKSLabelZoneFromARMZone returns the zone value in format of <region>-<zone-id>.
func MakeAKSLabelZoneFromARMZone(location string, zoneID string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(location), zoneID)
}

// MakeAKSLabelZoneFromARMZones returns the AKS zone label value from an ARM zones array.
// Returns Regional ("0") if the zones array is empty or nil (regional VM).
// Returns an error if there are multiple zones.
func MakeAKSLabelZoneFromARMZones(location string, zones []*string) (string, error) {
	if len(zones) == 0 || zones[0] == nil {
		return Regional, nil
	}
	if len(zones) == 1 {
		if location == "" {
			return "", fmt.Errorf("location is required for zonal resource")
		}
		return MakeAKSLabelZoneFromARMZone(location, *zones[0]), nil
	}
	return "", fmt.Errorf("resource has multiple zones")
}

// MakeARMZonesFromAKSLabelZone returns the zone ID from <region>-<zone-id>.
// Regional VMs (zone="0") return an empty slice so the VM
// is created without a zone assignment.
func MakeARMZonesFromAKSLabelZone(z string) []*string {
	if z == Regional {
		return []*string{}
	}
	zoneNum := z[len(z)-1:]
	return []*string{&zoneNum}
}

// MakeAKSLabelZoneFromVM returns the zone for the given virtual machine, or Regional ("0") if there is no zone specified.
// This matches the topology.kubernetes.io/zone label that AKS places on regional (non-zonal) nodes.
func MakeAKSLabelZoneFromVM(vm *armcompute.VirtualMachine) (string, error) {
	if vm == nil {
		return "", fmt.Errorf("cannot pass in a nil virtual machine")
	}
	return MakeAKSLabelZoneFromARMZones(lo.FromPtr(vm.Location), vm.Zones)
}
