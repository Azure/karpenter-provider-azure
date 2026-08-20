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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

func spotRequest() *FleetVMProvisionRequest {
	req := baseRequest()
	req.CapacityType = "spot"
	return req
}

func TestBuildFleetBody_SpotProfile(t *testing.T) {
	req := spotRequest()
	fleet := BuildFleetBody(req, 5, nil)

	require.NotNil(t, fleet.Properties.SpotPriorityProfile)
	assert.Nil(t, fleet.Properties.RegularPriorityProfile)

	spot := fleet.Properties.SpotPriorityProfile
	assert.Equal(t, int32(5), *spot.Capacity)
	assert.Equal(t, armcomputefleet.SpotAllocationStrategyPriceCapacityOptimized, *spot.AllocationStrategy)
	assert.Equal(t, armcomputefleet.EvictionPolicyDelete, *spot.EvictionPolicy)
	assert.Equal(t, false, *spot.Maintain)
	assert.Equal(t, float32(-1), *spot.MaxPricePerVM)
}

func TestBuildFleetBody_RegularProfile(t *testing.T) {
	req := baseRequest()
	fleet := BuildFleetBody(req, 3, nil)

	require.NotNil(t, fleet.Properties.RegularPriorityProfile)
	assert.Nil(t, fleet.Properties.SpotPriorityProfile)

	reg := fleet.Properties.RegularPriorityProfile
	assert.Equal(t, int32(3), *reg.Capacity)
	assert.Equal(t, armcomputefleet.RegularPriorityAllocationStrategyLowestPrice, *reg.AllocationStrategy)
	assert.Equal(t, int32(0), *reg.MinCapacity)
}

func TestBuildFleetBody_VMSizesProfileSorted(t *testing.T) {
	req := baseRequest()
	req.AcceptableSKUs = []string{"Standard_D8s_v3", "Standard_D2s_v3", "Standard_D4s_v3"}
	fleet := BuildFleetBody(req, 1, nil)

	names := make([]string, len(fleet.Properties.VMSizesProfile))
	for i, p := range fleet.Properties.VMSizesProfile {
		names[i] = *p.Name
	}
	assert.Equal(t, []string{"Standard_D2s_v3", "Standard_D4s_v3", "Standard_D8s_v3"}, names)
}

func TestBuildFleetBody_Tags(t *testing.T) {
	req := baseRequest()
	tags := map[string]*string{"key1": lo.ToPtr("val1"), "key2": lo.ToPtr("val2")}
	fleet := BuildFleetBody(req, 1, tags)

	assert.Equal(t, tags, fleet.Tags)
}

func TestBuildFleetBody_EncryptionAtHostNil(t *testing.T) {
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = nil
	fleet := BuildFleetBody(req, 1, nil)

	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	assert.Nil(t, bp.SecurityProfile)
}

func TestBuildFleetBody_EncryptionAtHostTrue(t *testing.T) {
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	fleet := BuildFleetBody(req, 1, nil)

	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	require.NotNil(t, bp.SecurityProfile)
	assert.Equal(t, true, *bp.SecurityProfile.EncryptionAtHost)
}

func TestBuildFleetBody_EncryptionAtHostFalse(t *testing.T) {
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(false)
	fleet := BuildFleetBody(req, 1, nil)

	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	assert.Nil(t, bp.SecurityProfile, "false should be treated same as nil - omitted")
}

func TestBuildFleetBody_DiskEncryptionSetID(t *testing.T) {
	req := baseRequest()
	req.DiskEncryptionSetID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/diskEncryptionSets/des1"
	fleet := BuildFleetBody(req, 1, nil)

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	require.NotNil(t, osDisk.ManagedDisk)
	require.NotNil(t, osDisk.ManagedDisk.DiskEncryptionSet)
	assert.Equal(t, req.DiskEncryptionSetID, *osDisk.ManagedDisk.DiskEncryptionSet.ID)
}

func TestBuildFleetBody_NoDiskEncryptionSetID(t *testing.T) {
	req := baseRequest()
	req.DiskEncryptionSetID = ""
	fleet := BuildFleetBody(req, 1, nil)

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	assert.Nil(t, osDisk.ManagedDisk)
}

func TestBuildFleetBody_NodeIdentities(t *testing.T) {
	req := baseRequest()
	req.NodeIdentities = []string{"/id/b", "/id/a"}
	fleet := BuildFleetBody(req, 1, nil)

	require.NotNil(t, fleet.Identity)
	assert.Equal(t, armcomputefleet.ManagedServiceIdentityTypeUserAssigned, *fleet.Identity.Type)
	assert.Contains(t, fleet.Identity.UserAssignedIdentities, "/id/a")
	assert.Contains(t, fleet.Identity.UserAssignedIdentities, "/id/b")
}

func TestBuildFleetBody_NetworkProfile(t *testing.T) {
	req := baseRequest()
	req.LBBackendPools = []string{"/pool/b", "/pool/a"}
	fleet := BuildFleetBody(req, 1, nil)

	np := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile
	require.NotNil(t, np)
	require.Len(t, np.NetworkInterfaceConfigurations, 1)

	nic := np.NetworkInterfaceConfigurations[0]
	assert.Equal(t, true, *nic.Properties.Primary)
	assert.Equal(t, true, *nic.Properties.EnableAcceleratedNetworking)

	ipConfig := nic.Properties.IPConfigurations[0]
	assert.Equal(t, req.LaunchTemplate.SubnetID, *ipConfig.Properties.Subnet.ID)

	// LB pools should be sorted
	require.Len(t, ipConfig.Properties.LoadBalancerBackendAddressPools, 2)
	assert.Equal(t, "/pool/a", *ipConfig.Properties.LoadBalancerBackendAddressPools[0].ID)
	assert.Equal(t, "/pool/b", *ipConfig.Properties.LoadBalancerBackendAddressPools[1].ID)
}

func TestBuildFleetBody_NSG(t *testing.T) {
	req := baseRequest()
	fleet := BuildFleetBody(req, 1, nil)

	nic := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0]
	require.NotNil(t, nic.Properties.NetworkSecurityGroup)
	assert.Equal(t, req.NSG, *nic.Properties.NetworkSecurityGroup.ID)
}

func TestBuildFleetBody_EphemeralDisk(t *testing.T) {
	req := baseRequest()
	req.LaunchTemplate.StorageProfileIsEphemeral = true
	req.LaunchTemplate.StorageProfilePlacement = armcompute.DiffDiskPlacementResourceDisk
	fleet := BuildFleetBody(req, 1, nil)

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	require.NotNil(t, osDisk.DiffDiskSettings)
	assert.Equal(t, armcomputefleet.DiffDiskOptionsLocal, *osDisk.DiffDiskSettings.Option)
	assert.Equal(t, armcomputefleet.DiffDiskPlacement(armcompute.DiffDiskPlacementResourceDisk), *osDisk.DiffDiskSettings.Placement)
	assert.Equal(t, armcomputefleet.CachingTypesReadOnly, *osDisk.Caching)
}

func TestConvertToScaleSetExtension(t *testing.T) {
	settings := map[string]any{"commandToExecute": "echo hello"}
	ext := &armcompute.VirtualMachineExtension{
		Name: lo.ToPtr("CSE"),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			Publisher:               lo.ToPtr("Microsoft.Azure.Extensions"),
			Type:                    lo.ToPtr("CustomScript"),
			TypeHandlerVersion:      lo.ToPtr("2.1"),
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Settings:                settings,
		},
	}

	result := ConvertToScaleSetExtension(ext)

	assert.Equal(t, "CSE", *result.Name)
	assert.Equal(t, "Microsoft.Azure.Extensions", *result.Properties.Publisher)
	assert.Equal(t, "CustomScript", *result.Properties.Type)
	assert.Equal(t, "2.1", *result.Properties.TypeHandlerVersion)
	assert.Equal(t, true, *result.Properties.AutoUpgradeMinorVersion)
	assert.Equal(t, settings, result.Properties.Settings)
}

func TestConvertToScaleSetExtension_NilInput(t *testing.T) {
	result := ConvertToScaleSetExtension(nil)
	assert.NotNil(t, result)
	assert.Nil(t, result.Properties)
}

func TestBuildFleetBody_Zones(t *testing.T) {
	req := baseRequest()
	req.AcceptableZones = []string{"3", "1"}
	fleet := BuildFleetBody(req, 1, nil)

	require.Len(t, fleet.Zones, 2)
	assert.Equal(t, "1", *fleet.Zones[0])
	assert.Equal(t, "3", *fleet.Zones[1])
}

func TestBuildFleetBody_NoZones(t *testing.T) {
	req := baseRequest()
	req.AcceptableZones = nil
	fleet := BuildFleetBody(req, 1, nil)

	assert.Nil(t, fleet.Zones)
}

func TestBuildFleetBody_CustomData(t *testing.T) {
	req := baseRequest()
	req.LaunchTemplate = &launchtemplate.Template{
		ImageID:              "/image",
		SubnetID:             "/subnet",
		ScriptlessCustomData: "base-custom-data",
		StorageProfileSizeGB: 128,
	}
	fleet := BuildFleetBody(req, 1, nil)

	osProfile := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.OSProfile
	assert.Equal(t, "base-custom-data", *osProfile.CustomData)
}

func TestBuildFleetBody_CustomScriptsCustomDataOverrides(t *testing.T) {
	req := baseRequest()
	req.LaunchTemplate = &launchtemplate.Template{
		ImageID:                 "/image",
		SubnetID:                "/subnet",
		ScriptlessCustomData:    "base-custom-data",
		CustomScriptsCustomData: "custom-scripts-data",
		StorageProfileSizeGB:    128,
	}
	fleet := BuildFleetBody(req, 1, nil)

	osProfile := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.OSProfile
	assert.Equal(t, "custom-scripts-data", *osProfile.CustomData)
}

func TestBuildFleetBody_ExtensionsViaProfile(t *testing.T) {
	req := baseRequest()
	settings := map[string]any{"cmd": "echo hi"}
	req.Extensions = []*armcompute.VirtualMachineExtension{
		{
			Name: lo.ToPtr("ext1"),
			Properties: &armcompute.VirtualMachineExtensionProperties{
				Publisher:          lo.ToPtr("Microsoft.Azure.Extensions"),
				Type:               lo.ToPtr("CustomScript"),
				TypeHandlerVersion: lo.ToPtr("2.1"),
				Settings:           settings,
			},
		},
		nil, // should be skipped
		{
			Name: lo.ToPtr("ext2"),
			Properties: &armcompute.VirtualMachineExtensionProperties{
				Publisher: lo.ToPtr("Microsoft.Compute"),
				Type:      lo.ToPtr("BGInfo"),
			},
		},
	}
	fleet := BuildFleetBody(req, 1, nil)

	ep := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile
	require.NotNil(t, ep)
	assert.Len(t, ep.Extensions, 2)
	assert.Equal(t, "ext1", *ep.Extensions[0].Name)
	assert.Equal(t, "ext2", *ep.Extensions[1].Name)
}

func TestBuildFleetBody_EmptyIdentitiesSkipped(t *testing.T) {
	req := baseRequest()
	req.NodeIdentities = []string{"", ""}
	fleet := BuildFleetBody(req, 1, nil)

	assert.Nil(t, fleet.Identity, "all-empty identity list should produce nil identity")
}

func TestToMapStringAny_PointerMap(t *testing.T) {
	m := map[string]interface{}{"key": "value"}
	result := toMapStringAny(&m)
	assert.Equal(t, map[string]any{"key": "value"}, result)
}

func TestToMapStringAny_NilPointer(t *testing.T) {
	var m *map[string]interface{}
	result := toMapStringAny(m)
	assert.Nil(t, result)
}

func TestToMapStringAny_UnknownType(t *testing.T) {
	result := toMapStringAny("not a map")
	assert.Nil(t, result)
}
