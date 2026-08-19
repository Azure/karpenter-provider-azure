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
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

func baseRequest() *FleetVMProvisionRequest {
	return &FleetVMProvisionRequest{
		NodeClaimName:   "test-claim-1",
		NodePoolName:    "default",
		CapacityType:    "on-demand",
		AcceptableSKUs:  []string{"Standard_D4s_v3", "Standard_D8s_v3"},
		AcceptableZones: []string{"2", "1"},
		Tags:            map[string]*string{"env": lo.ToPtr("test")},
		LaunchTemplate: &launchtemplate.Template{
			ImageID:              "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/galleries/gallery/images/image/versions/1.0.0",
			SubnetID:             "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet1",
			ScriptlessCustomData: "Y3VzdG9tZGF0YQ==",
			StorageProfileSizeGB: 128,
		},
		SSHPublicKey:   "ssh-rsa AAAAB3...",
		AdminUsername:  "azureuser",
		NodeIdentities: []string{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"},
		NSG:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg1",
		Location:       "eastus",
	}
}

func batchKey(req *FleetVMProvisionRequest) (string, error) {
	body := BuildFleetBody(req, 1, nil)
	return DetermineBatchKey(req.NodePoolName, body)
}

func TestDetermineBatchKey_Deterministic(t *testing.T) {
	req := baseRequest()
	key1, err := batchKey(req)
	require.NoError(t, err)

	key2, err := batchKey(req)
	require.NoError(t, err)

	assert.Equal(t, key1, key2, "same inputs must produce same key")
}

func TestDetermineBatchKey_Format(t *testing.T) {
	req := baseRequest()
	key, err := batchKey(req)
	require.NoError(t, err)

	assert.Regexp(t, `^default/[0-9a-f]{16}$`, key)
}

func TestDetermineBatchKey_DifferentCapacityType(t *testing.T) {
	req1 := baseRequest()
	req1.CapacityType = "on-demand"

	req2 := baseRequest()
	req2.CapacityType = "spot"

	key1, err := batchKey(req1)
	require.NoError(t, err)
	key2, err := batchKey(req2)
	require.NoError(t, err)

	assert.NotEqual(t, key1, key2, "different capacity types must produce different keys")
}

func TestDetermineBatchKey_DifferentSKUs(t *testing.T) {
	req1 := baseRequest()
	req1.AcceptableSKUs = []string{"Standard_D4s_v3"}

	req2 := baseRequest()
	req2.AcceptableSKUs = []string{"Standard_D8s_v3"}

	key1, err := batchKey(req1)
	require.NoError(t, err)
	key2, err := batchKey(req2)
	require.NoError(t, err)

	assert.NotEqual(t, key1, key2, "different SKU sets must produce different keys")
}

func TestDetermineBatchKey_SKUOrderIrrelevant(t *testing.T) {
	req1 := baseRequest()
	req1.AcceptableSKUs = []string{"Standard_D8s_v3", "Standard_D4s_v3"}

	req2 := baseRequest()
	req2.AcceptableSKUs = []string{"Standard_D4s_v3", "Standard_D8s_v3"}

	key1, err := batchKey(req1)
	require.NoError(t, err)
	key2, err := batchKey(req2)
	require.NoError(t, err)

	assert.Equal(t, key1, key2, "SKU order must not affect the key")
}

func TestDetermineBatchKey_ZoneOrderIrrelevant(t *testing.T) {
	req1 := baseRequest()
	req1.AcceptableZones = []string{"3", "1", "2"}

	req2 := baseRequest()
	req2.AcceptableZones = []string{"1", "2", "3"}

	key1, err := batchKey(req1)
	require.NoError(t, err)
	key2, err := batchKey(req2)
	require.NoError(t, err)

	assert.Equal(t, key1, key2, "zone order must not affect the key")
}

func TestDetermineBatchKey_EncryptionAtHostAffectsKey(t *testing.T) {
	req1 := baseRequest()
	// nil EncryptionAtHost

	req2 := baseRequest()
	req2.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)

	key1, err := batchKey(req1)
	require.NoError(t, err)
	key2, err := batchKey(req2)
	require.NoError(t, err)

	assert.NotEqual(t, key1, key2, "EncryptionAtHost nil vs true must produce different keys")
}

func TestDetermineBatchKey_NilFleetBody(t *testing.T) {
	_, err := DetermineBatchKey("pool", nil)
	assert.Error(t, err)
}

func TestDetermineBatchKey_NodeClaimNameDoesNotAffectKey(t *testing.T) {
	req1 := baseRequest()
	req1.NodeClaimName = "claim-a"

	req2 := baseRequest()
	req2.NodeClaimName = "claim-b"

	key1, err := batchKey(req1)
	require.NoError(t, err)
	key2, err := batchKey(req2)
	require.NoError(t, err)

	assert.Equal(t, key1, key2, "NodeClaimName is per-VM identity, must not affect batch key")
}
