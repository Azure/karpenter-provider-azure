package instance

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-kusto-go/kusto/kql"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
)

const (
	vmResourceType  = "microsoft.compute/virtualmachines"
	nicResourceType = "microsoft.network/networkinterfaces"
)

var (
	vmListQuery  string
	nicListQuery string
)

// getResourceListQueryBuilder returns a KQL query builder for listing resources with nodepool tags
func getResourceListQueryBuilder(rg string, resourceType string) *kql.Builder {
	return kql.New(`Resources`).
		AddLiteral(` | where type == `).AddString(resourceType).
		AddLiteral(` | where resourceGroup == `).AddString(strings.ToLower(rg)). // ARG resources appear to have lowercase RG
		AddLiteral(` | where tags has_cs `).AddString(NodePoolTagKey)
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
