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
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// --- Test Helpers ---

// defaultFields returns a BatchKeyFields with reasonable defaults for testing.
func defaultFields() BatchKeyFields {
	return BatchKeyFields{
		NodePoolName:        "default",
		CapacityType:        "on-demand",
		ImageID:             "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/galleries/g/images/i/versions/v",
		SubnetID:            "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/sn",
		SSHPublicKey:        "ssh-rsa AAAA...",
		AdminUsername:       "azureuser",
		CustomData:          "Y3VzdG9tZGF0YQ==",
		OSDiskSizeGB:        128,
		OSDiskType:          "",
		EncryptionAtHost:    false,
		DiskEncryptionSetID: "",
		NodeIdentities:      "",
		NSG:                 "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/nsg",
		CandidateSKUs:       []string{"Standard_D4s_v3", "Standard_D8s_v3"},
		Zones:               []string{"1", "2", "3"},
	}
}

// defaultTags returns a minimal tag map including the karpenter markers.
func defaultTags() map[string]*string {
	return map[string]*string{
		"karpenter.azure.com_managed-by":    lo.ToPtr("karpenter"),
		"karpenter.azure.com_batch-key-hash": lo.ToPtr("abcdef0123456789"),
	}
}

// mkInstanceTypes builds a map of instance types where all support accelerated networking.
func mkInstanceTypes(skus ...string) map[string]*corecloudprovider.InstanceType {
	m := make(map[string]*corecloudprovider.InstanceType, len(skus))
	for _, sku := range skus {
		m[sku] = &corecloudprovider.InstanceType{
			Name: sku,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1beta1.LabelSKUAcceleratedNetworking, corev1.NodeSelectorOpIn, "true"),
			),
		}
	}
	return m
}

// mkInstanceTypesNoAN builds instance types that do NOT support accelerated networking.
func mkInstanceTypesNoAN(skus ...string) map[string]*corecloudprovider.InstanceType {
	m := make(map[string]*corecloudprovider.InstanceType, len(skus))
	for _, sku := range skus {
		m[sku] = &corecloudprovider.InstanceType{
			Name: sku,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1beta1.LabelSKUAcceleratedNetworking, corev1.NodeSelectorOpIn, "false"),
			),
		}
	}
	return m
}

// --- Tests ---

// TestBuildFleetBody_SpotCapacityType verifies that a spot capacity type produces only
// a SpotPriorityProfile with PriceCapacityOptimized allocation, maintain=false, and
// EvictionPolicy=Delete. The RegularPriorityProfile must be nil.
func TestBuildFleetBody_SpotCapacityType(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "spot"

	fleet := BuildFleetBody(fields, 5, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties)
	require.NotNil(t, fleet.Properties.SpotPriorityProfile)
	assert.Nil(t, fleet.Properties.RegularPriorityProfile)
	assert.Equal(t, lo.ToPtr(armcomputefleet.SpotAllocationStrategyPriceCapacityOptimized), fleet.Properties.SpotPriorityProfile.AllocationStrategy)
	assert.Equal(t, lo.ToPtr(false), fleet.Properties.SpotPriorityProfile.Maintain)
	assert.Equal(t, lo.ToPtr(armcomputefleet.EvictionPolicyDelete), fleet.Properties.SpotPriorityProfile.EvictionPolicy)
}

// TestBuildFleetBody_OnDemandCapacityType verifies that an on-demand capacity type produces only
// a RegularPriorityProfile with LowestPrice allocation. The SpotPriorityProfile must be nil.
func TestBuildFleetBody_OnDemandCapacityType(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "on-demand"

	fleet := BuildFleetBody(fields, 3, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties)
	require.NotNil(t, fleet.Properties.RegularPriorityProfile)
	assert.Nil(t, fleet.Properties.SpotPriorityProfile)
	assert.Equal(t, lo.ToPtr(armcomputefleet.RegularPriorityAllocationStrategyLowestPrice), fleet.Properties.RegularPriorityProfile.AllocationStrategy)
}

// TestBuildFleetBody_SpotMaxPriceDefault verifies that when spotMaxPrice is nil,
// the spot profile defaults MaxPricePerVM to -1 (willing to pay up to on-demand price).
func TestBuildFleetBody_SpotMaxPriceDefault(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "spot"

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties.SpotPriorityProfile)
	require.NotNil(t, fleet.Properties.SpotPriorityProfile.MaxPricePerVM)
	assert.Equal(t, float32(-1), *fleet.Properties.SpotPriorityProfile.MaxPricePerVM)
}

// TestBuildFleetBody_SpotMaxPriceExplicit verifies that an explicit spotMaxPrice is
// preserved exactly on the spot profile.
func TestBuildFleetBody_SpotMaxPriceExplicit(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "spot"
	price := float32(0.5)

	fleet := BuildFleetBody(fields, 1, defaultTags(), &price, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties.SpotPriorityProfile)
	require.NotNil(t, fleet.Properties.SpotPriorityProfile.MaxPricePerVM)
	assert.Equal(t, float32(0.5), *fleet.Properties.SpotPriorityProfile.MaxPricePerVM)
}

// TestBuildFleetBody_VMSizesProfilePopulated verifies that VMSizesProfile has one entry
// per candidate SKU, in order, and that Rank is always nil for POC.
func TestBuildFleetBody_VMSizesProfilePopulated(t *testing.T) {
	fields := defaultFields()
	fields.CandidateSKUs = []string{"Standard_D2s_v3", "Standard_D4s_v3", "Standard_D8s_v3"}

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D2s_v3", "Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties.VMSizesProfile)
	assert.Len(t, fleet.Properties.VMSizesProfile, 3)
	for i, expected := range fields.CandidateSKUs {
		assert.Equal(t, expected, *fleet.Properties.VMSizesProfile[i].Name)
		assert.Nil(t, fleet.Properties.VMSizesProfile[i].Rank, "Rank must be nil for POC")
	}
}

// TestBuildFleetBody_TagsCarryThrough verifies that the tag map passed to BuildFleetBody
// appears verbatim on the Fleet body. Tags are caller's responsibility; we just pass through.
func TestBuildFleetBody_TagsCarryThrough(t *testing.T) {
	fields := defaultFields()
	tags := map[string]*string{
		"karpenter.azure.com_managed-by":    lo.ToPtr("karpenter"),
		"karpenter.azure.com_batch-key-hash": lo.ToPtr("deadbeef12345678"),
		"env": lo.ToPtr("test"),
	}

	fleet := BuildFleetBody(fields, 1, tags, nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	assert.Equal(t, tags, fleet.Tags)
}

// TestBuildFleetBody_EncryptionAtHostTrue verifies that when EncryptionAtHost is true,
// the SecurityProfile is present with EncryptionAtHost=true.
func TestBuildFleetBody_EncryptionAtHostTrue(t *testing.T) {
	fields := defaultFields()
	fields.EncryptionAtHost = true

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	baseProfile := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	require.NotNil(t, baseProfile.SecurityProfile)
	assert.Equal(t, lo.ToPtr(true), baseProfile.SecurityProfile.EncryptionAtHost)
}

// TestBuildFleetBody_EncryptionAtHostFalse verifies that when EncryptionAtHost is false,
// the SecurityProfile is nil (not set), matching the VM path behaviour.
func TestBuildFleetBody_EncryptionAtHostFalse(t *testing.T) {
	fields := defaultFields()
	fields.EncryptionAtHost = false

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	baseProfile := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	assert.Nil(t, baseProfile.SecurityProfile)
}

// TestBuildFleetBody_NodeIdentitiesPopulated verifies that when NodeIdentities is a
// non-empty comma-joined string, the Identity field is set with UserAssigned type and
// one entry per identity resource ID.
func TestBuildFleetBody_NodeIdentitiesPopulated(t *testing.T) {
	fields := defaultFields()
	fields.NodeIdentities = "/sub/rg/identity1,/sub/rg/identity2"

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Identity)
	assert.Equal(t, lo.ToPtr(armcomputefleet.ManagedServiceIdentityTypeUserAssigned), fleet.Identity.Type)
	assert.Len(t, fleet.Identity.UserAssignedIdentities, 2)
	assert.Contains(t, fleet.Identity.UserAssignedIdentities, "/sub/rg/identity1")
	assert.Contains(t, fleet.Identity.UserAssignedIdentities, "/sub/rg/identity2")
}

// TestBuildFleetBody_NodeIdentitiesEmpty verifies that when NodeIdentities is empty,
// the Identity field is nil (Fleet will use system-assigned or no identity).
func TestBuildFleetBody_NodeIdentitiesEmpty(t *testing.T) {
	fields := defaultFields()
	fields.NodeIdentities = ""

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	assert.Nil(t, fleet.Identity)
}

// TestBuildFleetBody_NetworkProfile_Basic verifies the network profile has one NIC config,
// one IP config, subnet is set, NSG is set, and accelerated networking matches expectations.
func TestBuildFleetBody_NetworkProfile_Basic(t *testing.T) {
	fields := defaultFields()

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	np := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile
	require.NotNil(t, np)
	// NetworkAPIVersion must be set when NetworkInterfaceConfigurations is used —
	// ARM rejects the request with NetworkApiVersionMustBeSpecifiedWithNetworkInterfaceConfigurations
	// (VMSS Flexible orchestration requirement).
	require.NotNil(t, np.NetworkAPIVersion, "NetworkAPIVersion must be set when NetworkInterfaceConfigurations is used")
	assert.Equal(t, armcomputefleet.NetworkAPIVersionV20201101, *np.NetworkAPIVersion)
	require.Len(t, np.NetworkInterfaceConfigurations, 1)
	nic := np.NetworkInterfaceConfigurations[0]
	assert.Equal(t, lo.ToPtr("nic"), nic.Name)
	require.NotNil(t, nic.Properties)
	assert.Equal(t, lo.ToPtr(true), nic.Properties.Primary)
	require.Len(t, nic.Properties.IPConfigurations, 1)
	ipConfig := nic.Properties.IPConfigurations[0]
	assert.Equal(t, lo.ToPtr(fields.SubnetID), ipConfig.Properties.Subnet.ID)
	require.NotNil(t, nic.Properties.NetworkSecurityGroup)
	assert.Equal(t, lo.ToPtr(fields.NSG), nic.Properties.NetworkSecurityGroup.ID)
}

// TestBuildFleetBody_NetworkProfile_NoNSG verifies that when fields.NSG is empty,
// the NetworkSecurityGroup SubResource is omitted (nil).
func TestBuildFleetBody_NetworkProfile_NoNSG(t *testing.T) {
	fields := defaultFields()
	fields.NSG = ""

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	nic := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0]
	assert.Nil(t, nic.Properties.NetworkSecurityGroup)
}

// TestBuildFleetBody_NetworkProfile_LBPools verifies that load balancer backend pool IDs
// are converted to SubResource references in the IP configuration.
func TestBuildFleetBody_NetworkProfile_LBPools(t *testing.T) {
	fields := defaultFields()
	pools := []string{"/sub/rg/lb/pool1", "/sub/rg/lb/pool2"}

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", pools, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	ipConfig := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.
		NetworkInterfaceConfigurations[0].Properties.IPConfigurations[0]
	require.Len(t, ipConfig.Properties.LoadBalancerBackendAddressPools, 2)
	assert.Equal(t, lo.ToPtr("/sub/rg/lb/pool1"), ipConfig.Properties.LoadBalancerBackendAddressPools[0].ID)
	assert.Equal(t, lo.ToPtr("/sub/rg/lb/pool2"), ipConfig.Properties.LoadBalancerBackendAddressPools[1].ID)
}

// TestBuildFleetBody_AcceleratedNetworking_AllSupport verifies that when all candidate SKUs
// support accelerated networking, EnableAcceleratedNetworking is true on the NIC.
func TestBuildFleetBody_AcceleratedNetworking_AllSupport(t *testing.T) {
	fields := defaultFields()
	fields.CandidateSKUs = []string{"Standard_D4s_v3", "Standard_D8s_v3"}
	its := mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3") // all support AN

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, its, true, nil)

	nic := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0]
	assert.Equal(t, lo.ToPtr(true), nic.Properties.EnableAcceleratedNetworking)
}

// TestBuildFleetBody_AcceleratedNetworking_OneMissing verifies that when at least one
// candidate SKU does NOT support accelerated networking, the conservative AND disables it.
// This prevents Fleet from failing to create VMs of that SKU with AN=true.
func TestBuildFleetBody_AcceleratedNetworking_OneMissing(t *testing.T) {
	fields := defaultFields()
	fields.CandidateSKUs = []string{"Standard_D4s_v3", "Standard_A2_v2"}
	its := map[string]*corecloudprovider.InstanceType{
		"Standard_D4s_v3": mkInstanceTypes("Standard_D4s_v3")["Standard_D4s_v3"],
		"Standard_A2_v2":  mkInstanceTypesNoAN("Standard_A2_v2")["Standard_A2_v2"],
	}

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, its, true, nil)

	nic := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0]
	assert.Equal(t, lo.ToPtr(false), nic.Properties.EnableAcceleratedNetworking)
}

// TestBuildFleetBody_ImageReference_SIG verifies that when useSIG=true, the image reference
// uses the ID field (Shared Image Gallery format) and CommunityGalleryImageID is nil.
func TestBuildFleetBody_ImageReference_SIG(t *testing.T) {
	fields := defaultFields()

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	imgRef := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.ImageReference
	require.NotNil(t, imgRef.ID)
	assert.Equal(t, fields.ImageID, *imgRef.ID)
	assert.Nil(t, imgRef.CommunityGalleryImageID)
}

// TestBuildFleetBody_ImageReference_CommunityGallery verifies that when useSIG=false,
// the image reference uses CommunityGalleryImageID and ID is nil.
func TestBuildFleetBody_ImageReference_CommunityGallery(t *testing.T) {
	fields := defaultFields()

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), false, nil)

	imgRef := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.ImageReference
	assert.Nil(t, imgRef.ID)
	require.NotNil(t, imgRef.CommunityGalleryImageID)
	assert.Equal(t, fields.ImageID, *imgRef.CommunityGalleryImageID)
}

// TestBuildFleetBody_OSDisk_Ephemeral verifies that when OSDiskType is set (e.g. "CacheDisk"),
// DiffDiskSettings is configured with Local option and the correct placement, and Caching=ReadOnly.
func TestBuildFleetBody_OSDisk_Ephemeral(t *testing.T) {
	fields := defaultFields()
	fields.OSDiskType = "CacheDisk"

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	require.NotNil(t, osDisk.DiffDiskSettings)
	assert.Equal(t, lo.ToPtr(armcomputefleet.DiffDiskOptionsLocal), osDisk.DiffDiskSettings.Option)
	assert.Equal(t, lo.ToPtr(armcomputefleet.DiffDiskPlacement("CacheDisk")), osDisk.DiffDiskSettings.Placement)
	assert.Equal(t, lo.ToPtr(armcomputefleet.CachingTypesReadOnly), osDisk.Caching)
}

// TestBuildFleetBody_OSDisk_Managed verifies that when OSDiskType is empty (managed disk),
// no DiffDiskSettings are present and Caching is not overridden.
func TestBuildFleetBody_OSDisk_Managed(t *testing.T) {
	fields := defaultFields()
	fields.OSDiskType = ""

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	assert.Nil(t, osDisk.DiffDiskSettings)
	assert.Nil(t, osDisk.Caching)
}

// TestBuildFleetBody_OSDisk_EncryptionSet verifies that when DiskEncryptionSetID is set,
// the ManagedDisk property includes a DiskEncryptionSet reference.
func TestBuildFleetBody_OSDisk_EncryptionSet(t *testing.T) {
	fields := defaultFields()
	fields.DiskEncryptionSetID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/diskEncryptionSets/des"

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	require.NotNil(t, osDisk.ManagedDisk)
	require.NotNil(t, osDisk.ManagedDisk.DiskEncryptionSet)
	assert.Equal(t, fields.DiskEncryptionSetID, *osDisk.ManagedDisk.DiskEncryptionSet.ID)
	// StorageAccountType must be set whenever ManagedDisk is — ARM defaults Standard_LRS
	// but the canonical SDK example and reference templates set it explicitly. Be defensive.
	require.NotNil(t, osDisk.ManagedDisk.StorageAccountType, "StorageAccountType must be set on ManagedDisk")
	assert.Equal(t, armcomputefleet.StorageAccountTypesStandardLRS, *osDisk.ManagedDisk.StorageAccountType)
}

// TestBuildFleetBody_Capacity verifies that targetCapacity is correctly mapped to the
// appropriate priority profile's Capacity field.
func TestBuildFleetBody_Capacity(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "on-demand"

	fleet := BuildFleetBody(fields, 7, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties.RegularPriorityProfile.Capacity)
	assert.Equal(t, int32(7), *fleet.Properties.RegularPriorityProfile.Capacity)
}

// TestBuildFleetBody_LocationAndZones verifies that Location and Zones are populated
// correctly on the Fleet body. Input zones are in AKS-label format ("<region>-<zone-id>")
// and are converted to the bare ARM zone IDs ("<zone-id>") required by the Fleet API.
// Zones order is preserved (already sorted by batchkey).
func TestBuildFleetBody_LocationAndZones(t *testing.T) {
	fields := defaultFields()
	fields.Zones = []string{"westus2-1", "westus2-2", "westus2-3"}

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "westus2", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	assert.Equal(t, lo.ToPtr("westus2"), fleet.Location)
	require.Len(t, fleet.Zones, 3)
	assert.Equal(t, "1", *fleet.Zones[0])
	assert.Equal(t, "2", *fleet.Zones[1])
	assert.Equal(t, "3", *fleet.Zones[2])
}

// TestBuildFleetBody_RegionalZoneIsDropped verifies that the Regional placeholder ("0")
// is filtered out before sending zones to the Fleet API (the API rejects "0" as invalid).
// When only Regional is present, Zones must be nil so Fleet creates a regional (non-zonal) deployment.
func TestBuildFleetBody_RegionalZoneIsDropped(t *testing.T) {
	fields := defaultFields()
	fields.Zones = []string{"0"}

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "westus2", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	assert.Nil(t, fleet.Zones, "Regional ('0') zone should be dropped, resulting in a regional Fleet")
}

// TestBuildFleetBody_UnknownCapacityType_DefaultsToRegular verifies that an unrecognized
// capacity type falls back to the regular profile rather than panicking. This is a defensive
// default — batch key has already separated by type, so this branch is unreachable in normal
// operation, but a panic here would crash the batcher goroutine.
func TestBuildFleetBody_UnknownCapacityType_DefaultsToRegular(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "garbage-type"

	fleet := BuildFleetBody(fields, 2, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	require.NotNil(t, fleet.Properties.RegularPriorityProfile)
	assert.Nil(t, fleet.Properties.SpotPriorityProfile)
	assert.Equal(t, int32(2), *fleet.Properties.RegularPriorityProfile.Capacity)
}

// TestBuildFleetBody_NoExtensions_NoExtensionProfile verifies that when extensions is nil/empty,
// the ExtensionProfile is nil. This allows this item to land before fleet-poc-mh-extensions-in-body.
func TestBuildFleetBody_NoExtensions_NoExtensionProfile(t *testing.T) {
	fields := defaultFields()

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	assert.Nil(t, fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile)
}

// TestBuildFleetBody_WithExtensions_ConvertedAndAttached verifies that when extensions are
// provided, they are converted to VMSS scale-set extensions and attached in the ExtensionProfile.
// This test validates the structural conversion; correctness of individual extension field
// mapping is the responsibility of fleet-poc-mh-extensions-in-body's tests.
func TestBuildFleetBody_WithExtensions_ConvertedAndAttached(t *testing.T) {

	fields := defaultFields()
	exts := []*armcompute.VirtualMachineExtension{
		{
			Name: lo.ToPtr("CSE"),
			Properties: &armcompute.VirtualMachineExtensionProperties{
				Publisher:          lo.ToPtr("Microsoft.Azure.Extensions"),
				Type:               lo.ToPtr("CustomScript"),
				TypeHandlerVersion: lo.ToPtr("2.1"),
				Settings:           map[string]any{"commandToExecute": "echo hello"},
			},
		},
	}

	fleet := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, exts)

	require.NotNil(t, fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile)
	assert.Len(t, fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile.Extensions, 1)
}

// TestBuildFleetBody_WithExtensions_PointerToMap verifies conversion of *map[string]interface{} settings
// which is the pattern used by the real extension constructors (getCSExtension, buildCSExtensionForFleet).
func TestBuildFleetBody_WithExtensions_PointerToMap(t *testing.T) {
	fields := defaultFields()
	exts := []*armcompute.VirtualMachineExtension{
		{
			Name: lo.ToPtr("cse-agent-karpenter"),
			Properties: &armcompute.VirtualMachineExtensionProperties{
				Publisher:               lo.ToPtr("Microsoft.Azure.Extensions"),
				Type:                    lo.ToPtr("CustomScript"),
				TypeHandlerVersion:      lo.ToPtr("2.0"),
				AutoUpgradeMinorVersion: lo.ToPtr(true),
				Settings:                &map[string]interface{}{},
				ProtectedSettings: &map[string]interface{}{
					"commandToExecute": "echo bootstrap",
				},
			},
		},
	}

	f := BuildFleetBody(fields, 1, defaultTags(), nil, "eastus", nil, mkInstanceTypes("Standard_D4s_v3"), false, exts)

	require.NotNil(t, f.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile)
	ext := f.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile.Extensions[0]
	assert.Equal(t, "cse-agent-karpenter", *ext.Name)
	assert.Equal(t, "Microsoft.Azure.Extensions", *ext.Properties.Publisher)
	assert.Equal(t, "CustomScript", *ext.Properties.Type)
	// ProtectedSettings should have been unwrapped from *map to map
	require.NotNil(t, ext.Properties.ProtectedSettings)
	assert.Equal(t, "echo bootstrap", ext.Properties.ProtectedSettings["commandToExecute"])
}

// TestBuildFleetBody_RoundTripMarshal verifies that the generated Fleet body can be
// serialized to JSON and deserialized back without loss. This is the closest we can get
// to SDK acceptance validation without hitting the real API — it catches wrong types,
// nil-pointer serialization issues, and omitempty dropping required fields.
func TestBuildFleetBody_RoundTripMarshal(t *testing.T) {
	fields := defaultFields()
	fields.CapacityType = "spot"
	fields.OSDiskType = "CacheDisk"
	fields.EncryptionAtHost = true
	fields.DiskEncryptionSetID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/diskEncryptionSets/des"
	fields.NodeIdentities = "/sub/rg/id1,/sub/rg/id2"
	price := float32(0.75)
	pools := []string{"/sub/rg/lb/pool1"}

	original := BuildFleetBody(fields, 5, defaultTags(), &price, "eastus2",
		pools, mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"), true, nil)

	// Marshal → JSON
	data, err := json.Marshal(original)
	require.NoError(t, err, "Fleet body must marshal to JSON without error")
	require.NotEmpty(t, data)

	// Unmarshal → fresh struct
	var roundTripped armcomputefleet.Fleet
	err = json.Unmarshal(data, &roundTripped)
	require.NoError(t, err, "Fleet body JSON must unmarshal back into armcomputefleet.Fleet")

	// Verify key fields survived the round trip
	assert.Equal(t, *original.Location, *roundTripped.Location)
	assert.Equal(t, len(original.Zones), len(roundTripped.Zones))
	assert.Equal(t, len(original.Tags), len(roundTripped.Tags))

	// Identity
	require.NotNil(t, roundTripped.Identity)
	assert.Equal(t, *original.Identity.Type, *roundTripped.Identity.Type)
	assert.Equal(t, len(original.Identity.UserAssignedIdentities), len(roundTripped.Identity.UserAssignedIdentities))

	// Spot profile
	require.NotNil(t, roundTripped.Properties.SpotPriorityProfile)
	assert.Equal(t, *original.Properties.SpotPriorityProfile.Capacity, *roundTripped.Properties.SpotPriorityProfile.Capacity)
	assert.Equal(t, *original.Properties.SpotPriorityProfile.MaxPricePerVM, *roundTripped.Properties.SpotPriorityProfile.MaxPricePerVM)

	// VM sizes
	assert.Equal(t, len(original.Properties.VMSizesProfile), len(roundTripped.Properties.VMSizesProfile))

	// Storage — ephemeral + encryption set
	rtOSDisk := roundTripped.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	require.NotNil(t, rtOSDisk.DiffDiskSettings)
	assert.Equal(t, *original.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk.DiffDiskSettings.Placement,
		*rtOSDisk.DiffDiskSettings.Placement)
	require.NotNil(t, rtOSDisk.ManagedDisk)
	assert.Equal(t, fields.DiskEncryptionSetID, *rtOSDisk.ManagedDisk.DiskEncryptionSet.ID)

	// Security
	require.NotNil(t, roundTripped.Properties.ComputeProfile.BaseVirtualMachineProfile.SecurityProfile)
	assert.Equal(t, true, *roundTripped.Properties.ComputeProfile.BaseVirtualMachineProfile.SecurityProfile.EncryptionAtHost)

	// Network — LB pools
	rtNP := roundTripped.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile
	require.NotNil(t, rtNP.NetworkAPIVersion, "NetworkAPIVersion must survive JSON round-trip")
	assert.Equal(t, armcomputefleet.NetworkAPIVersionV20201101, *rtNP.NetworkAPIVersion)
	rtIPConfig := rtNP.NetworkInterfaceConfigurations[0].Properties.IPConfigurations[0]
	require.Len(t, rtIPConfig.Properties.LoadBalancerBackendAddressPools, 1)
}
