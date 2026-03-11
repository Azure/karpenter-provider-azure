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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMachineTemplate_ToAzureMachine_RoundTrip(t *testing.T) {
	t.Parallel()

	tmpl := &MachineTemplate{
		Shared: SharedMachineConfig{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_D2s_v3"),
			},
			Priority:         lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
			NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-202503.04.0"),
			Network: &armcontainerservice.MachineNetworkProperties{
				VnetSubnetID: lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"),
			},
			Security: &armcontainerservice.MachineSecurityProfile{
				SSHAccess: lo.ToPtr(armcontainerservice.AgentPoolSSHAccessLocalUser),
			},
		},
		PerMachine: PerMachineConfig{
			MachineName: "nc-abc123",
			Zones:       []*string{lo.ToPtr("1")},
			Tags: map[string]*string{
				"karpenter.sh/nodeclaim": lo.ToPtr("nc-abc123"),
				"karpenter.sh/nodepool":  lo.ToPtr("default"),
			},
		},
	}

	machine := tmpl.ToAzureMachine()

	// Verify shared fields are present
	require.NotNil(t, machine.Properties)
	assert.Equal(t, "Standard_D2s_v3", lo.FromPtr(machine.Properties.Hardware.VMSize))
	assert.Equal(t, armcontainerservice.ScaleSetPriorityRegular, lo.FromPtr(machine.Properties.Priority))
	assert.Equal(t, "AKSUbuntu-2204gen2containerd-202503.04.0", lo.FromPtr(machine.Properties.NodeImageVersion))

	// Verify per-machine fields are present
	require.Len(t, machine.Zones, 1)
	assert.Equal(t, "1", lo.FromPtr(machine.Zones[0]))
	assert.Equal(t, "nc-abc123", lo.FromPtr(machine.Properties.Tags["karpenter.sh/nodeclaim"]))
	assert.Equal(t, "default", lo.FromPtr(machine.Properties.Tags["karpenter.sh/nodepool"]))

	// Verify security
	assert.Equal(t, armcontainerservice.AgentPoolSSHAccessLocalUser, lo.FromPtr(machine.Properties.Security.SSHAccess))
}

func TestMachineTemplate_SharedProperties_ExcludesPerMachineFields(t *testing.T) {
	t.Parallel()

	tmpl := &MachineTemplate{
		Shared: SharedMachineConfig{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_D4s_v3"),
			},
			Priority: lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot),
		},
		PerMachine: PerMachineConfig{
			MachineName: "nc-xyz789",
			Zones:       []*string{lo.ToPtr("2")},
			Tags: map[string]*string{
				"unique-tag": lo.ToPtr("value"),
			},
		},
	}

	props := tmpl.SharedProperties()

	// Shared fields present
	assert.Equal(t, "Standard_D4s_v3", lo.FromPtr(props.Hardware.VMSize))
	assert.Equal(t, armcontainerservice.ScaleSetPrioritySpot, lo.FromPtr(props.Priority))

	// Per-machine fields NOT present (Tags is on MachineProperties, so it should be nil)
	assert.Nil(t, props.Tags, "SharedProperties() must not include per-machine Tags")
}

func TestMachineTemplate_ToAzureMachine_NilFields(t *testing.T) {
	t.Parallel()

	tmpl := &MachineTemplate{
		Shared:     SharedMachineConfig{},
		PerMachine: PerMachineConfig{},
	}

	machine := tmpl.ToAzureMachine()

	// Should not panic on nil fields
	require.NotNil(t, machine.Properties)
	assert.Nil(t, machine.Properties.Hardware)
	assert.Nil(t, machine.Properties.Tags)
	assert.Nil(t, machine.Zones)
}

func TestMachineTemplate_SharedProperties_UsedForBatchHashing(t *testing.T) {
	t.Parallel()

	// Two templates with same shared config but different per-machine config
	// should produce identical SharedProperties
	tmpl1 := &MachineTemplate{
		Shared: SharedMachineConfig{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_D2s_v3"),
			},
		},
		PerMachine: PerMachineConfig{
			MachineName: "machine-1",
			Zones:       []*string{lo.ToPtr("1")},
			Tags:        map[string]*string{"id": lo.ToPtr("1")},
		},
	}
	tmpl2 := &MachineTemplate{
		Shared: SharedMachineConfig{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: lo.ToPtr("Standard_D2s_v3"),
			},
		},
		PerMachine: PerMachineConfig{
			MachineName: "machine-2",
			Zones:       []*string{lo.ToPtr("2")},
			Tags:        map[string]*string{"id": lo.ToPtr("2")},
		},
	}

	props1 := tmpl1.SharedProperties()
	props2 := tmpl2.SharedProperties()

	// The shared properties should be structurally identical
	assert.Equal(t, lo.FromPtr(props1.Hardware.VMSize), lo.FromPtr(props2.Hardware.VMSize))
	assert.Nil(t, props1.Tags)
	assert.Nil(t, props2.Tags)
}
