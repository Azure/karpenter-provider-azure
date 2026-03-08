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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBatchAwareness_PerMachineFieldsDoNotAffectGrouping verifies that two
// machine templates differing only in per-machine fields (fields that vary
// per NodeClaim, like Tags) produce the same grouping hash.
//
// This is Proposal C from the PR #1455 review discussion: a test co-located
// with the template builder (buildAKSMachineTemplate) that catches regressions
// in the batch field classification. If a developer adds a new per-NodeClaim
// field to the template builder but forgets to register it in
// ClearPerMachineFields, this test will fail because the two templates will
// hash differently.
//
// Known gap: offerings-related fields like Machine.Zones are per-machine but
// come from the scheduler's zone selection, not from NodeClaim data. They live
// on Machine (not MachineProperties) and are excluded from hashing by design.
// This test focuses on MachineProperties fields only.
func TestBatchAwareness_PerMachineFieldsDoNotAffectGrouping(t *testing.T) {
	t.Parallel()

	// Two templates that share all "shared config" fields but differ in per-machine fields.
	// This simulates two NodeClaims in the same NodePool+NodeClass being batched together.
	sharedProps := func(tags map[string]*string) armcontainerservice.MachineProperties {
		return armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_D4s_v3"),
			},
			Kubernetes: &armcontainerservice.MachineKubernetesProfile{
				OrchestratorVersion: lo.ToPtr("1.31.0"),
				NodeLabels: map[string]*string{
					"karpenter.sh_nodepool":      lo.ToPtr("default"),
					"karpenter.sh_capacity-type": lo.ToPtr("on-demand"),
				},
				MaxPods: lo.ToPtr[int32](250),
			},
			OperatingSystem: &armcontainerservice.MachineOSProfile{
				OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
				OSSKU:        lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
				OSDiskSizeGB: lo.ToPtr[int32](128),
				OSDiskType:   lo.ToPtr(armcontainerservice.OSDiskTypeEphemeral),
			},
			Network: &armcontainerservice.MachineNetworkProperties{
				VnetSubnetID: lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/nodesubnet"),
			},
			Priority:         lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
			Mode:             lo.ToPtr(armcontainerservice.AgentPoolModeUser),
			NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-2024.12.15"),
			Security: &armcontainerservice.MachineSecurityProfile{
				SSHAccess: lo.ToPtr(armcontainerservice.AgentPoolSSHAccessLocalUser),
			},
			// Per-machine: Tags differ between the two templates
			Tags: tags,
		}
	}

	// Template for NodeClaim "nc-aaa" — unique tags
	props1 := sharedProps(map[string]*string{
		"karpenter.azure.com_cluster":              lo.ToPtr("prod-cluster"),
		"karpenter.sh_nodeclaim":                   lo.ToPtr("nc-aaa"),
		"karpenter.sh_managed-by":                  lo.ToPtr("prod-cluster"),
		"kubernetes.azure.com_karpenter-timestamp":  lo.ToPtr("2024-01-01T00:00:00Z"),
	})

	// Template for NodeClaim "nc-bbb" — different unique tags
	props2 := sharedProps(map[string]*string{
		"karpenter.azure.com_cluster":              lo.ToPtr("prod-cluster"),
		"karpenter.sh_nodeclaim":                   lo.ToPtr("nc-bbb"),
		"karpenter.sh_managed-by":                  lo.ToPtr("prod-cluster"),
		"kubernetes.azure.com_karpenter-timestamp":  lo.ToPtr("2024-01-01T00:00:01Z"),
	})

	// Hash both templates after clearing per-machine and read-only fields
	// (same logic as batch/grouper.go computeTemplateHash)
	hash1 := hashProperties(&props1)
	hash2 := hashProperties(&props2)

	assert.Equal(t, hash1, hash2,
		"Templates differing only in per-machine fields (Tags) should produce the same hash. "+
			"If this fails, a per-machine field was added to the template without registering it "+
			"in ClearPerMachineFields (batch_field_registry.go)")
}

// TestBatchAwareness_SharedFieldsAffectGrouping verifies that templates with
// different shared configuration fields produce different hashes, ensuring that
// VMs with incompatible configs never land in the same batch.
func TestBatchAwareness_SharedFieldsAffectGrouping(t *testing.T) {
	t.Parallel()

	baseProps := func() armcontainerservice.MachineProperties {
		return armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_D4s_v3"),
			},
			Kubernetes: &armcontainerservice.MachineKubernetesProfile{
				OrchestratorVersion: lo.ToPtr("1.31.0"),
			},
			OperatingSystem: &armcontainerservice.MachineOSProfile{
				OSType: lo.ToPtr(armcontainerservice.OSTypeLinux),
				OSSKU:  lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
			},
			Priority:         lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
			Mode:             lo.ToPtr(armcontainerservice.AgentPoolModeUser),
			NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-2024.12.15"),
		}
	}

	tests := []struct {
		name   string
		modify func(props *armcontainerservice.MachineProperties)
	}{
		{
			name: "different VM size",
			modify: func(props *armcontainerservice.MachineProperties) {
				props.Hardware.VMSize = lo.ToPtr("Standard_D8s_v3")
			},
		},
		{
			name: "different priority (spot)",
			modify: func(props *armcontainerservice.MachineProperties) {
				props.Priority = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
			},
		},
		{
			name: "different OS SKU",
			modify: func(props *armcontainerservice.MachineProperties) {
				props.OperatingSystem.OSSKU = lo.ToPtr(armcontainerservice.OSSKUAzureLinux)
			},
		},
		{
			name: "different K8s version",
			modify: func(props *armcontainerservice.MachineProperties) {
				props.Kubernetes.OrchestratorVersion = lo.ToPtr("1.30.0")
			},
		},
	}

	baseHash := hashProperties(lo.ToPtr(baseProps()))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			modified := baseProps()
			tt.modify(&modified)
			modifiedHash := hashProperties(&modified)
			assert.NotEqual(t, baseHash, modifiedHash,
				"Templates with different shared config (%s) must produce different hashes", tt.name)
		})
	}
}

// TestClearPerMachineFields_NilsTags verifies the basic contract.
func TestClearPerMachineFields_NilsTags(t *testing.T) {
	t.Parallel()
	props := armcontainerservice.MachineProperties{
		Tags:     map[string]*string{"k": lo.ToPtr("v")},
		Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D4s_v3")},
	}
	ClearPerMachineFields(&props)
	assert.Nil(t, props.Tags, "ClearPerMachineFields should nil Tags")
	assert.NotNil(t, props.Hardware, "ClearPerMachineFields should not touch Hardware")
}

// TestClearReadOnlyFields_NilsServerSetFields verifies the basic contract.
func TestClearReadOnlyFields_NilsServerSetFields(t *testing.T) {
	t.Parallel()
	props := armcontainerservice.MachineProperties{
		ETag:              lo.ToPtr("etag"),
		ProvisioningState: lo.ToPtr("Succeeded"),
		ResourceID:        lo.ToPtr("/sub/rg/..."),
		Status:            &armcontainerservice.MachineStatus{},
		Hardware:          &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D4s_v3")},
	}
	ClearReadOnlyFields(&props)
	assert.Nil(t, props.ETag)
	assert.Nil(t, props.ProvisioningState)
	assert.Nil(t, props.ResourceID)
	assert.Nil(t, props.Status)
	assert.NotNil(t, props.Hardware, "ClearReadOnlyFields should not touch Hardware")
}

// hashProperties mirrors the hashing logic from batch/grouper.go computeTemplateHash,
// minus the Machine-level fields (Zones, Name) which are handled separately.
func hashProperties(props *armcontainerservice.MachineProperties) string {
	cleared := *props
	ClearPerMachineFields(&cleared)
	ClearReadOnlyFields(&cleared)

	jsonBytes, err := json.Marshal(cleared)
	if err != nil {
		jsonBytes = []byte(fmt.Sprintf("%+v", cleared))
	}
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash[:8])
}

// TestHashProperties_Deterministic ensures the helper function is deterministic.
func TestHashProperties_Deterministic(t *testing.T) {
	t.Parallel()
	props := armcontainerservice.MachineProperties{
		Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D4s_v3")},
		Tags:     map[string]*string{"k": lo.ToPtr("v")},
	}
	h1 := hashProperties(&props)
	h2 := hashProperties(&props)
	require.Equal(t, h1, h2, "hashProperties must be deterministic")
	require.NotEmpty(t, h1)
}
