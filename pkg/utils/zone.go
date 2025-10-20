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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
)

// MakeZone returns the zone value in format of <region>-<zone-id>.
func MakeZone(location string, zoneID string) string {
	if zoneID == "" {
		return ""
	}
	return fmt.Sprintf("%s-%s", strings.ToLower(location), zoneID)
}

// VM Zones field expects just the zone number, without region
func MakeVMZone(zone string) []*string {
	if zone == "" {
		return []*string{}
	}
	zoneNum := zone[len(zone)-1:]
	return []*string{&zoneNum}
}

// GetZone returns the zone for the given virtual machine, or an empty string if there is no zone specified
func GetZone(vm *armcompute.VirtualMachine) (string, error) {
	if vm == nil {
		return "", fmt.Errorf("cannot pass in a nil virtual machine")
	}
	if vm.Zones == nil {
		return "", nil
	}
	if len(vm.Zones) == 1 {
		if vm.Location == nil {
			return "", fmt.Errorf("virtual machine is missing location")
		}
		return MakeZone(*vm.Location, *(vm.Zones)[0]), nil
	}
	if len(vm.Zones) > 1 {
		return "", fmt.Errorf("virtual machine has multiple zones")
	}
	return "", nil
}
