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
	"fmt"
	"maps"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute" //nolint:staticcheck
	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

func spotRequest() *FleetVMProvisionRequest {
	req := baseRequest()
	req.CapacityType = "spot"
	return req
}

func TestBuildFleetBody_SpotProfile(t *testing.T) {
	g := NewWithT(t)
	req := spotRequest()
	fleet, err := BuildFleetBody(req, 5, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Properties.SpotPriorityProfile).ToNot(BeNil())
	g.Expect(fleet.Properties.RegularPriorityProfile).To(BeNil())

	spot := fleet.Properties.SpotPriorityProfile
	g.Expect(*spot.Capacity).To(Equal(int32(5)))
	g.Expect(*spot.AllocationStrategy).To(Equal(armcomputefleet.SpotAllocationStrategyPriceCapacityOptimized))
	g.Expect(*spot.EvictionPolicy).To(Equal(armcomputefleet.EvictionPolicyDelete))
	g.Expect(*spot.Maintain).To(BeFalse())
	g.Expect(*spot.MaxPricePerVM).To(Equal(float32(-1)))
}

func TestBuildFleetBody_RegularProfile(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	fleet, err := BuildFleetBody(req, 3, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Properties.RegularPriorityProfile).ToNot(BeNil())
	g.Expect(fleet.Properties.SpotPriorityProfile).To(BeNil())

	reg := fleet.Properties.RegularPriorityProfile
	g.Expect(*reg.Capacity).To(Equal(int32(3)))
	g.Expect(*reg.AllocationStrategy).To(Equal(armcomputefleet.RegularPriorityAllocationStrategyLowestPrice))
	g.Expect(*reg.MinCapacity).To(Equal(int32(0)))
}

func TestBuildFleetBody_VMSizesProfileSorted(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.AcceptableSKUs = []string{"Standard_D8s_v3", "Standard_D2s_v3", "Standard_D4s_v3"}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	names := make([]string, len(fleet.Properties.VMSizesProfile))
	for i, p := range fleet.Properties.VMSizesProfile {
		names[i] = *p.Name
	}
	g.Expect(names).To(Equal([]string{"Standard_D2s_v3", "Standard_D4s_v3", "Standard_D8s_v3"}))
}

func TestBuildFleetBody_Tags(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.Tags[launchtemplate.NodePoolTagKey] = lo.ToPtr(req.NodePoolName)
	req.Tags[launchtemplate.KarpenterManagedTagKey] = lo.ToPtr("test-cluster")
	req.Tags[launchtemplate.BillingTagKey] = lo.ToPtr(launchtemplate.BillingTagValueLinux)
	req.LaunchTemplate.Tags = maps.Clone(req.Tags)
	originalTags := maps.Clone(req.Tags)

	tags := maps.Clone(req.Tags)
	tags["karpenter.azure.com_fleet-name"] = lo.ToPtr("fleet-123")
	tags["karpenter.azure.com_managed-by"] = lo.ToPtr("aks")
	fleet, err := BuildFleetBody(req, 1, tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Tags).To(Equal(tags))
	g.Expect(fleet.Tags).To(HaveKeyWithValue(launchtemplate.NodePoolTagKey, lo.ToPtr(req.NodePoolName)))
	g.Expect(fleet.Tags).To(HaveKeyWithValue("karpenter.azure.com_fleet-name", lo.ToPtr("fleet-123")))
	g.Expect(fleet.Tags).To(HaveKeyWithValue("karpenter.azure.com_managed-by", lo.ToPtr("aks")))
	g.Expect(req.Tags).To(Equal(originalTags))
	g.Expect(req.LaunchTemplate.Tags).To(Equal(originalTags))
}

func TestBuildFleetBody_NilTags(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	fleet, err := BuildFleetBody(req, 1, nil)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Tags).To(BeNil())
	g.Expect(req.Tags).NotTo(BeEmpty())
	g.Expect(req.LaunchTemplate.Tags).NotTo(BeEmpty())
}

func TestBuildFleetBody_EncryptionAtHostNil(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = nil
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	g.Expect(bp.SecurityProfile).To(BeNil())
	g.Expect(req.LaunchTemplate.EncryptionAtHost).To(BeNil())
}

func TestBuildFleetBody_EncryptionAtHostTrue(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	g.Expect(bp.SecurityProfile).ToNot(BeNil())
	g.Expect(*bp.SecurityProfile.EncryptionAtHost).To(BeTrue())
}

func TestBuildFleetBody_EncryptionAtHostFalse(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(false)
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	g.Expect(bp.SecurityProfile).ToNot(BeNil())
	g.Expect(bp.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
	g.Expect(*bp.SecurityProfile.EncryptionAtHost).To(BeFalse())
	g.Expect(req.LaunchTemplate.EncryptionAtHost).To(Equal(lo.ToPtr(false)))
}

func TestBuildFleetBody_DiskEncryptionSetID(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.DiskEncryptionSetID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/diskEncryptionSets/des1"
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	g.Expect(osDisk.ManagedDisk).ToNot(BeNil())
	g.Expect(osDisk.ManagedDisk.DiskEncryptionSet).ToNot(BeNil())
	g.Expect(*osDisk.ManagedDisk.DiskEncryptionSet.ID).To(Equal(req.DiskEncryptionSetID))
	g.Expect(osDisk.ManagedDisk.StorageAccountType).To(BeNil())
}

func TestBuildFleetBody_NoDiskEncryptionSetID(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.DiskEncryptionSetID = ""
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	g.Expect(osDisk.ManagedDisk).To(BeNil())
}

func TestBuildFleetBody_NodeIdentities(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.NodeIdentities = []string{"/id/b", "/id/a"}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Identity).ToNot(BeNil())
	g.Expect(*fleet.Identity.Type).To(Equal(armcomputefleet.ManagedServiceIdentityTypeUserAssigned))
	g.Expect(fleet.Identity.UserAssignedIdentities).To(HaveKey("/id/a"))
	g.Expect(fleet.Identity.UserAssignedIdentities).To(HaveKey("/id/b"))
}

func TestBuildFleetBody_NetworkProfile(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LBBackendPools = []string{"/pool/b", "/pool/a"}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	np := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile
	g.Expect(np).ToNot(BeNil())
	g.Expect(np.NetworkInterfaceConfigurations).To(HaveLen(1))

	nic := np.NetworkInterfaceConfigurations[0]
	g.Expect(*nic.Properties.Primary).To(BeTrue())
	g.Expect(*nic.Properties.EnableAcceleratedNetworking).To(BeTrue())
	g.Expect(nic.Properties.IPConfigurations).To(HaveLen(1))

	ipConfig := nic.Properties.IPConfigurations[0]
	g.Expect(*ipConfig.Properties.Subnet.ID).To(Equal(req.LaunchTemplate.SubnetID))

	// LB pools should be sorted
	g.Expect(ipConfig.Properties.LoadBalancerBackendAddressPools).To(HaveLen(2))
	g.Expect(*ipConfig.Properties.LoadBalancerBackendAddressPools[0].ID).To(Equal("/pool/a"))
	g.Expect(*ipConfig.Properties.LoadBalancerBackendAddressPools[1].ID).To(Equal("/pool/b"))
}

func TestBuildFleetBody_IPConfigurations(t *testing.T) {
	for _, count := range []int32{1, 2, 30, 250} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			g := NewWithT(t)
			req := baseRequest()
			req.NetworkPlugin = consts.NetworkPluginAzure
			req.NetworkPluginMode = consts.NetworkPluginModeNone
			req.MaxPods = count
			req.LBBackendPools = []string{"/pool/b", "/pool/a"}
			body, err := BuildFleetBody(req, 2, req.Tags)
			g.Expect(err).NotTo(HaveOccurred())

			data, err := json.Marshal(body)
			g.Expect(err).NotTo(HaveOccurred())
			var decoded armcomputefleet.Fleet
			g.Expect(json.Unmarshal(data, &decoded)).To(Succeed())
			network := decoded.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile
			g.Expect(network.NetworkInterfaceConfigurations).To(HaveLen(1))
			nic := network.NetworkInterfaceConfigurations[0].Properties
			g.Expect(*nic.Primary).To(BeTrue())
			g.Expect(*nic.EnableIPForwarding).To(BeFalse())
			g.Expect(*nic.EnableAcceleratedNetworking).To(BeTrue())
			g.Expect(*nic.NetworkSecurityGroup.ID).To(Equal(req.NSG))
			g.Expect(*nic.DeleteOption).To(Equal(armcomputefleet.DeleteOptionsDelete))
			g.Expect(nic.IPConfigurations).To(HaveLen(int(count)))
			for i, config := range nic.IPConfigurations {
				g.Expect(*config.Name).To(Equal(fmt.Sprintf("ipconfig%d", i+1)))
				g.Expect(*config.Properties.Primary).To(Equal(i == 0))
				g.Expect(*config.Properties.Subnet.ID).To(Equal(req.LaunchTemplate.SubnetID))
				if i == 0 {
					g.Expect(config.Properties.LoadBalancerBackendAddressPools).To(HaveLen(2))
					g.Expect(*config.Properties.LoadBalancerBackendAddressPools[0].ID).To(Equal("/pool/a"))
					g.Expect(*config.Properties.LoadBalancerBackendAddressPools[1].ID).To(Equal("/pool/b"))
				} else {
					g.Expect(config.Properties.LoadBalancerBackendAddressPools).To(BeEmpty())
				}
			}
			g.Expect(req.MaxPods).To(Equal(count))
			g.Expect(req.LBBackendPools).To(Equal([]string{"/pool/b", "/pool/a"}))
		})
	}
}

func TestBuildFleetBody_IPConfigurationsByNetworkMode(t *testing.T) {
	for _, tc := range []struct {
		name, plugin, mode string
		maxPods            int32
		expected           int
	}{
		{"node subnet", consts.NetworkPluginAzure, consts.NetworkPluginModeNone, 30, 30},
		{"node subnet custom", consts.NetworkPluginAzure, consts.NetworkPluginModeNone, 11, 11},
		{"overlay", consts.NetworkPluginAzure, consts.NetworkPluginModeOverlay, 250, 1},
		{"overlay custom", consts.NetworkPluginAzure, consts.NetworkPluginModeOverlay, 30, 1},
		{"kubenet", "kubenet", "", 110, 1},
		{"none", consts.NetworkPluginNone, "", 250, 1},
		{"unspecified", "", "", 0, 1},
		{"node subnet zero still has primary", consts.NetworkPluginAzure, "", 0, 1},
		{"node subnet negative still has primary", consts.NetworkPluginAzure, "", -1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			req := baseRequest()
			req.NetworkPlugin = tc.plugin
			req.NetworkPluginMode = tc.mode
			req.MaxPods = tc.maxPods
			body, err := BuildFleetBody(req, 1, req.Tags)
			g.Expect(err).NotTo(HaveOccurred())
			configs := body.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0].Properties.IPConfigurations
			g.Expect(configs).To(HaveLen(tc.expected))
			g.Expect(*configs[0].Properties.Primary).To(BeTrue())
		})
	}
}

func TestBuildFleetBody_NSG(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	nic := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0]
	g.Expect(nic.Properties.NetworkSecurityGroup).ToNot(BeNil())
	g.Expect(*nic.Properties.NetworkSecurityGroup.ID).To(Equal(req.NSG))
}

func TestBuildFleetBody_EphemeralDisk(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.StorageProfileIsEphemeral = true
	req.LaunchTemplate.StorageProfilePlacement = armcompute.DiffDiskPlacementResourceDisk
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	osDisk := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.OSDisk
	g.Expect(osDisk.DiffDiskSettings).ToNot(BeNil())
	g.Expect(*osDisk.DiffDiskSettings.Option).To(Equal(armcomputefleet.DiffDiskOptionsLocal))
	g.Expect(*osDisk.DiffDiskSettings.Placement).To(Equal(armcomputefleet.DiffDiskPlacement(armcompute.DiffDiskPlacementResourceDisk)))
	g.Expect(*osDisk.Caching).To(Equal(armcomputefleet.CachingTypesReadOnly))
}

func TestConvertToScaleSetExtension(t *testing.T) {
	g := NewWithT(t)
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

	g.Expect(*result.Name).To(Equal("CSE"))
	g.Expect(*result.Properties.Publisher).To(Equal("Microsoft.Azure.Extensions"))
	g.Expect(*result.Properties.Type).To(Equal("CustomScript"))
	g.Expect(*result.Properties.TypeHandlerVersion).To(Equal("2.1"))
	g.Expect(*result.Properties.AutoUpgradeMinorVersion).To(BeTrue())
	g.Expect(result.Properties.Settings).To(Equal(settings))
}

func TestConvertToScaleSetExtension_NilInput(t *testing.T) {
	g := NewWithT(t)
	result := ConvertToScaleSetExtension(nil)
	g.Expect(result).ToNot(BeNil())
	g.Expect(result.Properties).To(BeNil())
}

func TestBuildFleetBody_Zones(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.AcceptableZones = []string{"3", "1"}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Zones).To(HaveLen(2))
	g.Expect(*fleet.Zones[0]).To(Equal("1"))
	g.Expect(*fleet.Zones[1]).To(Equal("3"))
}

func TestBuildFleetBody_NoZones(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.AcceptableZones = nil
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Zones).To(BeNil())
}

func TestBuildFleetBody_CustomData(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate = &launchtemplate.Template{
		ImageID:              "/image",
		SubnetID:             "/subnet",
		ScriptlessCustomData: "base-custom-data",
		StorageProfileSizeGB: 128,
	}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	osProfile := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.OSProfile
	g.Expect(*osProfile.CustomData).To(Equal("base-custom-data"))
}

func TestBuildFleetBody_CustomScriptsCustomDataOverrides(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate = &launchtemplate.Template{
		ImageID:                 "/image",
		SubnetID:                "/subnet",
		ScriptlessCustomData:    "base-custom-data",
		CustomScriptsCustomData: "custom-scripts-data",
		StorageProfileSizeGB:    128,
	}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	osProfile := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.OSProfile
	g.Expect(*osProfile.CustomData).To(Equal("custom-scripts-data"))
}

func TestBuildFleetBody_ExtensionsViaProfile(t *testing.T) {
	g := NewWithT(t)
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
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	ep := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.ExtensionProfile
	g.Expect(ep).ToNot(BeNil())
	g.Expect(ep.Extensions).To(HaveLen(2))
	g.Expect(*ep.Extensions[0].Name).To(Equal("ext1"))
	g.Expect(*ep.Extensions[1].Name).To(Equal("ext2"))
}

func TestBuildFleetBody_EmptyIdentitiesSkipped(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.NodeIdentities = []string{"", ""}
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Identity).To(BeNil(), "all-empty identity list should produce nil identity")
}

func TestToMapStringAny_PointerMap(t *testing.T) {
	g := NewWithT(t)
	m := map[string]interface{}{"key": "value"}
	result := toMapStringAny(&m)
	g.Expect(result).To(Equal(map[string]any{"key": "value"}))
}

func TestBuildFleetBody_ImageReference(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	image := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile.StorageProfile.ImageReference
	g.Expect(image).NotTo(BeNil())
	g.Expect(image.ID).To(BeNil())
	g.Expect(image.SharedGalleryImageID).To(BeNil())
	g.Expect(image.CommunityGalleryImageID).To(Equal(lo.ToPtr(req.LaunchTemplate.ImageID)))
}

func TestBuildFleetBody_AcceleratedNetworking(t *testing.T) {
	for _, capability := range []string{skewer.AcceleratedNetworking, skewer.EncryptionAtHost} {
		t.Run(capability, func(t *testing.T) {
			for _, tt := range []struct {
				name           string
				candidateIndex int
				capability     compute.ResourceSkuCapabilities
				omitCapability bool
				extraSKU       bool
				enabled        bool
			}{
				{name: "all supported", capability: compute.ResourceSkuCapabilities{Value: lo.ToPtr("True")}, enabled: true},
				{name: "first unsupported", capability: compute.ResourceSkuCapabilities{Value: lo.ToPtr("False")}},
				{name: "last unsupported", candidateIndex: 1, capability: compute.ResourceSkuCapabilities{Value: lo.ToPtr("False")}},
				{name: "missing capability", omitCapability: true},
				{name: "nil capability value", capability: compute.ResourceSkuCapabilities{}},
				{name: "malformed capability", capability: compute.ResourceSkuCapabilities{Value: lo.ToPtr("not-a-bool")}},
				{name: "case insensitive true", capability: compute.ResourceSkuCapabilities{Value: lo.ToPtr("tRuE")}, enabled: true},
				{name: "extra unsupported SKU", extraSKU: true, capability: compute.ResourceSkuCapabilities{Value: lo.ToPtr("False")}, enabled: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					g := NewWithT(t)
					req := baseRequest()
					req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
					name := req.AcceptableSKUs[tt.candidateIndex]
					if tt.extraSKU {
						name = "Standard_D2s_v3"
					}
					sku := req.ResolvedSKUs[name]
					capabilities := []compute.ResourceSkuCapabilities{}
					for _, existing := range *sku.Capabilities {
						if *existing.Name != capability {
							capabilities = append(capabilities, existing)
						}
					}
					if !tt.omitCapability {
						value := tt.capability
						value.Name = lo.ToPtr(capability)
						capabilities = append(capabilities, value)
					}
					sku.Capabilities = &capabilities
					before, err := json.Marshal(req)
					g.Expect(err).ToNot(HaveOccurred())
					desiredEncryption := req.LaunchTemplate.EncryptionAtHost

					fleet, err := BuildFleetBody(req, 1, req.Tags)
					g.Expect(err).ToNot(HaveOccurred())

					names := []string{}
					for _, profile := range fleet.Properties.VMSizesProfile {
						names = append(names, *profile.Name)
					}
					expectedNames := req.AcceptableSKUs
					if capability == skewer.EncryptionAtHost && !tt.enabled {
						expectedNames = []string{req.AcceptableSKUs[1-tt.candidateIndex]}
					}
					g.Expect(names).To(Equal(expectedNames))
					bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
					g.Expect(bp.NetworkProfile.NetworkInterfaceConfigurations).To(HaveLen(1))
					nic := bp.NetworkProfile.NetworkInterfaceConfigurations[0]
					g.Expect(nic.Properties.EnableAcceleratedNetworking).To(Equal(lo.ToPtr(capability != skewer.AcceleratedNetworking || tt.enabled)))
					g.Expect(bp.SecurityProfile).ToNot(BeNil())
					g.Expect(bp.SecurityProfile.EncryptionAtHost).To(Equal(lo.ToPtr(true)))
					after, err := json.Marshal(req)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(after).To(Equal(before), "building a fleet must not mutate the caller's request")
					g.Expect(req.LaunchTemplate.EncryptionAtHost).To(BeIdenticalTo(desiredEncryption))
				})
			}
		})
	}
}

func TestBuildFleetBody_IncompleteSKUCapabilities(t *testing.T) {
	for _, tt := range []struct {
		name          string
		mutate        func(*FleetVMProvisionRequest)
		expectedNames []string
		expectError   bool
	}{
		{name: "unknown candidate", expectedNames: []string{"Standard_D4s_v3", "Standard_D8s_v3"}, mutate: func(req *FleetVMProvisionRequest) {
			req.AcceptableSKUs = append(req.AcceptableSKUs, "unknown")
		}},
		{name: "missing SKU", expectedNames: []string{"Standard_D8s_v3"}, mutate: func(req *FleetVMProvisionRequest) {
			delete(req.ResolvedSKUs, req.AcceptableSKUs[0])
		}},
		{name: "nil SKU", expectedNames: []string{"Standard_D4s_v3"}, mutate: func(req *FleetVMProvisionRequest) {
			req.ResolvedSKUs[req.AcceptableSKUs[1]] = nil
		}},
		{name: "nil resolved SKUs", expectError: true, mutate: func(req *FleetVMProvisionRequest) {
			req.ResolvedSKUs = nil
		}},
		{name: "all missing SKUs", expectError: true, mutate: func(req *FleetVMProvisionRequest) {
			req.ResolvedSKUs = map[string]*skewer.SKU{}
		}},
		{name: "all nil SKUs", expectError: true, mutate: func(req *FleetVMProvisionRequest) {
			for _, name := range req.AcceptableSKUs {
				req.ResolvedSKUs[name] = nil
			}
		}},
		{name: "empty candidates", expectError: true, mutate: func(req *FleetVMProvisionRequest) {
			req.AcceptableSKUs = []string{}
		}},
		{name: "nil candidates", expectError: true, mutate: func(req *FleetVMProvisionRequest) {
			req.AcceptableSKUs = nil
		}},
		{name: "nil capabilities", expectedNames: []string{"Standard_D8s_v3"}, mutate: func(req *FleetVMProvisionRequest) {
			req.ResolvedSKUs[req.AcceptableSKUs[0]].Capabilities = nil
		}},
		{name: "empty capabilities", expectedNames: []string{"Standard_D8s_v3"}, mutate: func(req *FleetVMProvisionRequest) {
			req.ResolvedSKUs[req.AcceptableSKUs[0]].Capabilities = &[]compute.ResourceSkuCapabilities{}
		}},
		{name: "nil capability name", expectedNames: []string{"Standard_D8s_v3"}, mutate: func(req *FleetVMProvisionRequest) {
			req.ResolvedSKUs[req.AcceptableSKUs[0]].Capabilities = &[]compute.ResourceSkuCapabilities{
				{Name: nil, Value: lo.ToPtr("True")},
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			req := baseRequest()
			req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
			tt.mutate(req)
			before, err := json.Marshal(req)
			g.Expect(err).ToNot(HaveOccurred())

			fleet, err := BuildFleetBody(req, 1, req.Tags)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(fleet).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				names := []string{}
				for _, profile := range fleet.Properties.VMSizesProfile {
					names = append(names, *profile.Name)
				}
				g.Expect(names).To(Equal(tt.expectedNames))
				bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
				g.Expect(bp.NetworkProfile.NetworkInterfaceConfigurations).To(HaveLen(1))
				nic := bp.NetworkProfile.NetworkInterfaceConfigurations[0]
				g.Expect(nic.Properties.EnableAcceleratedNetworking).To(Equal(lo.ToPtr(true)))
				g.Expect(bp.SecurityProfile).ToNot(BeNil())
				g.Expect(bp.SecurityProfile.EncryptionAtHost).To(Equal(lo.ToPtr(true)))
			}
			after, err := json.Marshal(req)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(after).To(Equal(before), "building a fleet must not mutate the caller's request")
		})
	}
}

func TestBuildFleetBody_InvalidRequest(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*FleetVMProvisionRequest) *FleetVMProvisionRequest
	}{
		{name: "nil request", mutate: func(_ *FleetVMProvisionRequest) *FleetVMProvisionRequest {
			return nil
		}},
		{name: "nil template", mutate: func(req *FleetVMProvisionRequest) *FleetVMProvisionRequest {
			req.LaunchTemplate = nil
			return req
		}},
		{name: "no candidates", mutate: func(req *FleetVMProvisionRequest) *FleetVMProvisionRequest {
			req.AcceptableSKUs = nil
			return req
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			req := tt.mutate(baseRequest())

			fleet, err := BuildFleetBody(req, 1, nil)

			g.Expect(err).To(HaveOccurred())
			g.Expect(fleet).To(BeNil())
		})
	}
}

func TestBuildFleetBody_AllCandidatesLackRequiredEncryption(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	for _, name := range req.AcceptableSKUs {
		req.ResolvedSKUs[name].Capabilities = &[]compute.ResourceSkuCapabilities{
			{Name: lo.ToPtr(skewer.AcceleratedNetworking), Value: lo.ToPtr("True")},
			{Name: lo.ToPtr(skewer.EncryptionAtHost), Value: lo.ToPtr("False")},
		}
	}
	before, err := json.Marshal(req)
	g.Expect(err).ToNot(HaveOccurred())

	fleet, err := BuildFleetBody(req, 1, req.Tags)

	g.Expect(err).To(MatchError("building Fleet body: no candidate SKUs support requested encryption at host"))
	g.Expect(fleet).To(BeNil())
	after, err := json.Marshal(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(after).To(Equal(before))
}

func TestBuildFleetBody_OptionalEncryptionDoesNotFilterCandidates(t *testing.T) {
	for _, tt := range []struct {
		name       string
		encryption *bool
	}{
		{name: "nil"},
		{name: "false", encryption: lo.ToPtr(false)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			req := baseRequest()
			req.LaunchTemplate.EncryptionAtHost = tt.encryption
			req.ResolvedSKUs["Standard_D8s_v3"].Capabilities = &[]compute.ResourceSkuCapabilities{
				{Name: lo.ToPtr(skewer.AcceleratedNetworking), Value: lo.ToPtr("False")},
				{Name: lo.ToPtr(skewer.EncryptionAtHost), Value: lo.ToPtr("False")},
			}
			req.AcceptableSKUs = append(req.AcceptableSKUs, "unknown", "nil-entry")
			req.ResolvedSKUs["nil-entry"] = nil
			before, err := json.Marshal(req)
			g.Expect(err).ToNot(HaveOccurred())

			fleet, err := BuildFleetBody(req, 1, req.Tags)
			g.Expect(err).ToNot(HaveOccurred())

			names := []string{}
			for _, profile := range fleet.Properties.VMSizesProfile {
				names = append(names, *profile.Name)
			}
			g.Expect(names).To(ConsistOf(req.AcceptableSKUs))
			bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
			g.Expect(bp.NetworkProfile.NetworkInterfaceConfigurations).To(HaveLen(1))
			nic := bp.NetworkProfile.NetworkInterfaceConfigurations[0]
			g.Expect(nic.Properties.EnableAcceleratedNetworking).To(Equal(lo.ToPtr(false)))
			if tt.encryption == nil {
				g.Expect(bp.SecurityProfile).To(BeNil())
			} else {
				g.Expect(bp.SecurityProfile).ToNot(BeNil())
				g.Expect(bp.SecurityProfile.EncryptionAtHost).To(Equal(lo.ToPtr(false)))
			}
			after, err := json.Marshal(req)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(after).To(Equal(before))
			g.Expect(req.LaunchTemplate.EncryptionAtHost).To(BeIdenticalTo(tt.encryption))
		})
	}
}

func TestBuildFleetBody_AcceleratedNetworkingAfterEncryptionFiltering(t *testing.T) {
	g := NewWithT(t)
	req := baseRequest()
	req.LaunchTemplate.EncryptionAtHost = lo.ToPtr(true)
	req.ResolvedSKUs["Standard_D4s_v3"].Capabilities = &[]compute.ResourceSkuCapabilities{
		{Name: lo.ToPtr(skewer.AcceleratedNetworking), Value: lo.ToPtr("False")},
		{Name: lo.ToPtr(skewer.EncryptionAtHost), Value: lo.ToPtr("False")},
	}
	before, err := json.Marshal(req)
	g.Expect(err).ToNot(HaveOccurred())

	fleet, err := BuildFleetBody(req, 1, req.Tags)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(fleet.Properties.VMSizesProfile).To(HaveLen(1))
	g.Expect(fleet.Properties.VMSizesProfile[0].Name).To(Equal(lo.ToPtr("Standard_D8s_v3")))
	bp := fleet.Properties.ComputeProfile.BaseVirtualMachineProfile
	g.Expect(bp.NetworkProfile.NetworkInterfaceConfigurations).To(HaveLen(1))
	nic := bp.NetworkProfile.NetworkInterfaceConfigurations[0]
	g.Expect(nic.Properties.EnableAcceleratedNetworking).To(Equal(lo.ToPtr(true)))
	g.Expect(bp.SecurityProfile).ToNot(BeNil())
	g.Expect(bp.SecurityProfile.EncryptionAtHost).To(Equal(lo.ToPtr(true)))
	after, err := json.Marshal(req)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(after).To(Equal(before))
}

func TestToMapStringAny_NilPointer(t *testing.T) {
	g := NewWithT(t)
	var m *map[string]interface{}
	result := toMapStringAny(m)
	g.Expect(result).To(BeNil())
}

func TestToMapStringAny_UnknownType(t *testing.T) {
	g := NewWithT(t)
	result := toMapStringAny("not a map")
	g.Expect(result).To(BeNil())
}
