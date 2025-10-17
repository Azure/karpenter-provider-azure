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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-kusto-go/kusto/kql"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/samber/lo"
)

const (
	vmResourceType  = "microsoft.compute/virtualmachines"
	nicResourceType = "microsoft.network/networkinterfaces"
)

// getResourceListQueryBuilder returns a KQL query builder for listing resources with nodepool tags
func getResourceListQueryBuilder(rg string, resourceType string) *kql.Builder {
	return kql.New(`Resources`).
		AddLiteral(` | where type == `).AddString(resourceType).
		AddLiteral(` | where resourceGroup == `).AddString(strings.ToLower(rg)). // ARG resources appear to have lowercase RG
		AddLiteral(` | where tags has_cs `).AddString(launchtemplate.NodePoolTagKey)
}

// GetVMListQueryBuilder returns a KQL query builder for listing VMs with nodepool tags
func GetVMListQueryBuilder(rg string) *kql.Builder {
	return getResourceListQueryBuilder(rg, vmResourceType)
}

// GetNICListQueryBuilder returns a KQL query builder for listing NICs with nodepool tags
func GetNICListQueryBuilder(rg string) *kql.Builder {
	return getResourceListQueryBuilder(rg, nicResourceType)
}

// createVMFromQueryResponseData converts ARG query response data into a VirtualMachine object
func createVMFromQueryResponseData(data map[string]interface{}) (*armcompute.VirtualMachine, error) {
	jsonString, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	vm := armcompute.VirtualMachine{}
	err = json.Unmarshal(jsonString, &vm)
	if err != nil {
		return nil, err
	}
	if vm.ID == nil {
		return nil, fmt.Errorf("virtual machine is missing id")
	}
	if vm.Name == nil {
		return nil, fmt.Errorf("virtual machine is missing name")
	}
	if vm.Tags == nil {
		return nil, fmt.Errorf("virtual machine is missing tags")
	}
	// We see inconsistent casing being returned by ARG for the last segment
	// of the vm.ID string. This forces it to be lowercase.
	parts := strings.Split(lo.FromPtr(vm.ID), "/")
	parts[len(parts)-1] = strings.ToLower(parts[len(parts)-1])
	vm.ID = lo.ToPtr(strings.Join(parts, "/"))
	return &vm, nil
}

// createNICFromQueryResponseData converts ARG query response data into a Network Interface object
func createNICFromQueryResponseData(data map[string]interface{}) (*armnetwork.Interface, error) {
	jsonString, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	nic := armnetwork.Interface{}
	err = json.Unmarshal(jsonString, &nic)
	if err != nil {
		return nil, err
	}
	if nic.ID == nil {
		return nil, fmt.Errorf("network interface is missing id")
	}
	if nic.Name == nil {
		return nil, fmt.Errorf("network interface is missing name")
	}
	if nic.Tags == nil {
		return nil, fmt.Errorf("network interface is missing tags")
	}
	// We see inconsistent casing being returned by ARG for the last segment
	// of the nic.ID string. This forces it to be lowercase.
	parts := strings.Split(lo.FromPtr(nic.ID), "/")
	parts[len(parts)-1] = strings.ToLower(parts[len(parts)-1])
	nic.ID = lo.ToPtr(strings.Join(parts, "/"))
	return &nic, nil
}
