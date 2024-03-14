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
	"context"
	"fmt"
	"regexp"

	"sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"knative.dev/pkg/logging"
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
		log.FromContext(ctx).Error(err, "Failed to convert resource group name to lower case in providerID %s", providerID)
		// fallback to original providerID
		return providerID
	}
	return providerIDLowerRG
}

func MkVMID(resourceGroupName string, vmName string) string {
	const idFormat = "/subscriptions/subscriptionID/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s"
	return fmt.Sprintf(idFormat, resourceGroupName, vmName)
}

// TestLogger gets a logger to use in unit and end to end tests
func TestLogger(t zaptest.TestingT) *zap.SugaredLogger {
	opts := zaptest.WrapOptions(
		zap.AddCaller(),
		zap.Development(),
	)

	return zaptest.NewLogger(t, opts).Sugar()
}

// TestContextWithLogger returns a context with a logger to be used in tests
func TestContextWithLogger(t zaptest.TestingT) context.Context {
	return logging.WithLogger(context.Background(), TestLogger(t))
}
