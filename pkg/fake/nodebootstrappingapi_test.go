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

package fake

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/stretchr/testify/assert"
)

// Note that we want this to represent the real contract, rather than the fake
func TestNodeBootstrappingAPI_Get_Success(t *testing.T) {
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	nb, err := api.Get(context.TODO(), params)
	assert.NoError(t, err)

	// Suggestion: verify legitimacy of CSE + CustomData
	// However, given that Karpenter does not contain that knowledge, the validation logic should be imported from CRP, if there is a well-contained one
	// Even the omit of bootstrap token, where not having them in the first place is a valid case, and Karpenter's contract is to only replace only if they (the templated fields) are present

	_, err = base64.StdEncoding.DecodeString(nb.CustomDataEncodedDehydratable)
	assert.NoError(t, err) // CustomData should be encoded
	_, err = base64.StdEncoding.DecodeString(nb.CSEDehydratable)
	assert.Error(t, err) // CSE should not be encoded
}

func TestNodeBootstrappingAPI_Get_MissingProvisionProfile(t *testing.T) {
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      nil,
		ProvisionHelperValues: createValidProvisionHelperValues(),
	}

	_, err := api.Get(context.TODO(), params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "MissingRequiredProperty")
	assert.Contains(t, err.Error(), "ProvisionProfile cannot be empty")
}

func TestNodeBootstrappingAPI_Get_MissingProvisionHelperValues(t *testing.T) {
	api := &NodeBootstrappingAPI{}

	params := &models.ProvisionValues{
		ProvisionProfile:      createValidProvisionProfile(),
		ProvisionHelperValues: nil,
	}

	_, err := api.Get(context.TODO(), params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "MissingRequiredProperty")
	assert.Contains(t, err.Error(), "ProvisionHelperValues cannot be empty")
}

// Helper functions

func createValidProvisionProfile() *models.ProvisionProfile {
	name := "test-node"
	vmSize := "Standard_D2s_v3"
	osType := models.OSTypeLinux
	osSku := models.OSSKUAzureLinux
	storageProfile := "ManagedDisks"
	distro := "AzureLinux"
	orchestratorVersion := "1.26.0"
	vnetCidrs := []string{"10.0.0.0/8"}
	vnetSubnetID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
	mode := models.AgentPoolModeSystem
	architecture := "amd64"
	maxPods := int32(110)

	return &models.ProvisionProfile{
		Name:                &name,
		VMSize:              &vmSize,
		OsType:              &osType,
		OsSku:               &osSku,
		StorageProfile:      &storageProfile,
		Distro:              &distro,
		OrchestratorVersion: &orchestratorVersion,
		VnetCidrs:           vnetCidrs,
		VnetSubnetID:        &vnetSubnetID,
		Mode:                &mode,
		Architecture:        &architecture,
		MaxPods:             &maxPods,
	}
}

func createValidProvisionHelperValues() *models.ProvisionHelperValues {
	skuCPU := float64(2)
	skuMemory := float64(8192)

	return &models.ProvisionHelperValues{
		SkuCPU:    &skuCPU,
		SkuMemory: &skuMemory,
	}
}
