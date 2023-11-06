// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package utils

import (
	"context"
	"fmt"
	"regexp"

	"knative.dev/pkg/logging"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider"
)

// GetVMName parses the provider ID stored on the node to get the vmName
// associated with a node
func GetVMName(providerID string) (string, error) {
	// standalone VMs have providerID in the format: azure:///subscriptions/<subscriptionID>/resourceGroups/<resourceGroup>/providers/Microsoft.Compute/virtualMachines/<instanceID>
	r := regexp.MustCompile(`azure:///subscriptions/.*/resourceGroups/.*/providers/Microsoft.Compute/virtualMachines/(?P<InstanceID>.*)`)
	matches := r.FindStringSubmatch(providerID)
	if matches == nil {
		return "", fmt.Errorf("parsing vm name %s", providerID)
	}
	for i, name := range r.SubexpNames() {
		if name == "InstanceID" {
			return matches[i], nil
		}
	}
	return "", fmt.Errorf("parsing vm name %s", providerID)
}

func ResourceIDToProviderID(ctx context.Context, id string) string {
	providerID := fmt.Sprintf("azure://%s", id)
	// for historical reasons Azure providerID has the resource group name in lower case
	providerIDLowerRG, err := provider.ConvertResourceGroupNameToLower(providerID)
	if err != nil {
		logging.FromContext(ctx).Warnf("Failed to convert resource group name to lower case in providerID %s: %v", providerID, err)
		// fallback to original providerID
		return providerID
	}
	return providerIDLowerRG
}

func MkVMID(resourceGroupName string, vmName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName)
}
