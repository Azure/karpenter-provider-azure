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
	"fmt"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
)

const (
	vmNamePrefix       = "aks"
	computerNamePrefix = "aks-"
	nicConfigName      = "nic"
	ipConfigName       = "ipconfig1"
	sshKeyPathTemplate = "/home/%s/.ssh/authorized_keys"
)

// BuildFleetBody constructs the armcomputefleet.Fleet body from a provision request.
// Slices (SKUs, zones) are sorted internally for deterministic JSON serialization.
func BuildFleetBody(req *FleetVMProvisionRequest, targetCapacity int32, tags map[string]*string) *armcomputefleet.Fleet {
	lt := req.LaunchTemplate

	fleet := &armcomputefleet.Fleet{
		Location:   lo.ToPtr(req.Location),
		Tags:       tags,
		Zones:      buildZones(req.AcceptableZones),
		Identity:   buildIdentity(req.NodeIdentities),
		Properties: buildFleetProperties(req, lt, targetCapacity),
	}

	return fleet
}

// buildZones sorts and converts zone strings to ARM zone pointers. Returns nil for regional Fleet.
func buildZones(zones []string) []*string {
	if len(zones) == 0 {
		return nil
	}
	sortedZones := slices.Clone(zones)
	slices.Sort(sortedZones)

	zoneRefs := make([]*string, 0, len(sortedZones))
	for _, zone := range sortedZones {
		zoneRefs = append(zoneRefs, lo.ToPtr(zone))
	}
	return zoneRefs
}

func buildIdentity(identities []string) *armcomputefleet.ManagedServiceIdentity {
	if len(identities) == 0 {
		return nil
	}
	sortedIdentities := slices.Clone(identities)
	slices.Sort(sortedIdentities)

	identityMap := make(map[string]*armcomputefleet.UserAssignedIdentity, len(sortedIdentities))
	for _, identity := range sortedIdentities {
		if identity == "" {
			continue
		}
		identityMap[identity] = &armcomputefleet.UserAssignedIdentity{}
	}
	if len(identityMap) == 0 {
		return nil
	}
	return &armcomputefleet.ManagedServiceIdentity{
		Type:                   lo.ToPtr(armcomputefleet.ManagedServiceIdentityTypeUserAssigned),
		UserAssignedIdentities: identityMap,
	}
}

// buildFleetProperties assembles FleetProperties with the appropriate priority profile.
func buildFleetProperties(req *FleetVMProvisionRequest, lt *launchtemplate.Template, targetCapacity int32) *armcomputefleet.FleetProperties {
	props := &armcomputefleet.FleetProperties{
		VMSizesProfile: buildVMSizesProfile(req.AcceptableSKUs),
		ComputeProfile: buildComputeProfile(req, lt),
		Mode:           lo.ToPtr(armcomputefleet.FleetModeLaunch),
		VMNamePrefix:   lo.ToPtr(vmNamePrefix),
	}

	switch req.CapacityType {
	case karpv1.CapacityTypeSpot:
		props.SpotPriorityProfile = buildSpotProfile(targetCapacity)
	default:
		props.RegularPriorityProfile = buildRegularProfile(targetCapacity)
	}

	return props
}

// buildVMSizesProfile creates one VMSizeProfile entry per candidate SKU, sorted.
func buildVMSizesProfile(skus []string) []*armcomputefleet.VMSizeProfile {
	sortedSKUs := slices.Clone(skus)
	slices.Sort(sortedSKUs)

	profiles := make([]*armcomputefleet.VMSizeProfile, 0, len(sortedSKUs))
	for _, sku := range sortedSKUs {
		profiles = append(profiles, &armcomputefleet.VMSizeProfile{Name: lo.ToPtr(sku)})
	}
	return profiles
}

// buildSpotProfile constructs the spot priority profile.
func buildSpotProfile(capacity int32) *armcomputefleet.SpotPriorityProfile {
	return &armcomputefleet.SpotPriorityProfile{
		Capacity:           lo.ToPtr(capacity),
		AllocationStrategy: lo.ToPtr(armcomputefleet.SpotAllocationStrategyPriceCapacityOptimized),
		EvictionPolicy:     lo.ToPtr(armcomputefleet.EvictionPolicyDelete),
		Maintain:           lo.ToPtr(false),
		MaxPricePerVM:      lo.ToPtr(float32(-1)),
	}
}

// buildRegularProfile constructs the on-demand (regular) priority profile.
func buildRegularProfile(capacity int32) *armcomputefleet.RegularPriorityProfile {
	return &armcomputefleet.RegularPriorityProfile{
		Capacity:           lo.ToPtr(capacity),
		AllocationStrategy: lo.ToPtr(armcomputefleet.RegularPriorityAllocationStrategyLowestPrice),
		MinCapacity:        lo.ToPtr(int32(0)),
	}
}

// buildComputeProfile constructs the BaseVirtualMachineProfile.
func buildComputeProfile(req *FleetVMProvisionRequest, lt *launchtemplate.Template) *armcomputefleet.ComputeProfile {
	baseProfile := &armcomputefleet.BaseVirtualMachineProfile{
		OSProfile:        buildOSProfile(req, lt),
		StorageProfile:   buildStorageProfile(lt, req.DiskEncryptionSetID),
		NetworkProfile:   BuildFleetNetworkProfile(lt.SubnetID, req.NSG, req.LBBackendPools),
		SecurityProfile:  buildSecurityProfile(lt.EncryptionAtHost),
		ExtensionProfile: extensionsToProfile(req.Extensions),
	}

	return &armcomputefleet.ComputeProfile{
		BaseVirtualMachineProfile: baseProfile,
	}
}

// buildOSProfile constructs the Linux OS profile.
func buildOSProfile(req *FleetVMProvisionRequest, lt *launchtemplate.Template) *armcomputefleet.VirtualMachineScaleSetOSProfile {
	sshKeyPath := fmt.Sprintf(sshKeyPathTemplate, req.AdminUsername)

	osProfile := &armcomputefleet.VirtualMachineScaleSetOSProfile{
		AdminUsername:      lo.ToPtr(req.AdminUsername),
		ComputerNamePrefix: lo.ToPtr(computerNamePrefix),
		LinuxConfiguration: &armcomputefleet.LinuxConfiguration{
			DisablePasswordAuthentication: lo.ToPtr(true),
			SSH: &armcomputefleet.SSHConfiguration{
				PublicKeys: []*armcomputefleet.SSHPublicKey{{
					KeyData: lo.ToPtr(req.SSHPublicKey),
					Path:    lo.ToPtr(sshKeyPath),
				}},
			},
		},
	}

	customData := lt.ScriptlessCustomData
	if lt.CustomScriptsCustomData != "" {
		customData = lt.CustomScriptsCustomData
	}
	if customData != "" {
		osProfile.CustomData = lo.ToPtr(customData)
	}
	return osProfile
}

// buildStorageProfile constructs the OS disk and image reference.
func buildStorageProfile(lt *launchtemplate.Template, diskEncryptionSetID string) *armcomputefleet.VirtualMachineScaleSetStorageProfile {
	imageRef := &armcomputefleet.ImageReference{
		ID: lo.ToPtr(lt.ImageID),
	}

	osDisk := &armcomputefleet.VirtualMachineScaleSetOSDisk{
		CreateOption: lo.ToPtr(armcomputefleet.DiskCreateOptionTypesFromImage),
		DiskSizeGB:   lo.ToPtr(lt.StorageProfileSizeGB),
		OSType:       lo.ToPtr(armcomputefleet.OperatingSystemTypesLinux),
	}

	// Ephemeral disk
	if lt.StorageProfileIsEphemeral {
		osDisk.DiffDiskSettings = &armcomputefleet.DiffDiskSettings{
			Option:    lo.ToPtr(armcomputefleet.DiffDiskOptionsLocal),
			Placement: lo.ToPtr(armcomputefleet.DiffDiskPlacement(lt.StorageProfilePlacement)),
		}
		osDisk.Caching = lo.ToPtr(armcomputefleet.CachingTypesReadOnly)
	}

	// Disk encryption set
	if diskEncryptionSetID != "" {
		osDisk.ManagedDisk = &armcomputefleet.VirtualMachineScaleSetManagedDiskParameters{
			StorageAccountType: lo.ToPtr(armcomputefleet.StorageAccountTypesStandardLRS),
			DiskEncryptionSet: &armcomputefleet.DiskEncryptionSetParameters{
				ID: lo.ToPtr(diskEncryptionSetID),
			},
		}
	}

	return &armcomputefleet.VirtualMachineScaleSetStorageProfile{
		ImageReference: imageRef,
		OSDisk:         osDisk,
	}
}

// BuildFleetNetworkProfile constructs the VMSS network profile with subnet, NSG, and LB backend pools.
func BuildFleetNetworkProfile(subnetID, nsgID string, lbBackendPools []string) *armcomputefleet.VirtualMachineScaleSetNetworkProfile {
	nicProperties := &armcomputefleet.VirtualMachineScaleSetNetworkConfigurationProperties{
		Primary:                     lo.ToPtr(true),
		EnableAcceleratedNetworking: lo.ToPtr(true),
		EnableIPForwarding:          lo.ToPtr(false),
		DeleteOption:                lo.ToPtr(armcomputefleet.DeleteOptionsDelete),
		IPConfigurations: []*armcomputefleet.VirtualMachineScaleSetIPConfiguration{{
			Name: lo.ToPtr(ipConfigName),
			Properties: &armcomputefleet.VirtualMachineScaleSetIPConfigurationProperties{
				Primary:                         lo.ToPtr(true),
				Subnet:                          &armcomputefleet.APIEntityReference{ID: lo.ToPtr(subnetID)},
				LoadBalancerBackendAddressPools: buildPoolRefs(lbBackendPools),
			},
		}},
	}
	if nsgID != "" {
		nicProperties.NetworkSecurityGroup = &armcomputefleet.SubResource{ID: lo.ToPtr(nsgID)}
	}
	return &armcomputefleet.VirtualMachineScaleSetNetworkProfile{
		NetworkAPIVersion: lo.ToPtr(armcomputefleet.NetworkAPIVersionV20201101), // hardcoded; not available in LaunchTemplate
		NetworkInterfaceConfigurations: []*armcomputefleet.VirtualMachineScaleSetNetworkConfiguration{{
			Name:       lo.ToPtr(nicConfigName),
			Properties: nicProperties,
		}},
	}
}

// buildSecurityProfile returns the security profile when encryption at host is configured.
func buildSecurityProfile(encryptionAtHost *bool) *armcomputefleet.SecurityProfile {
	if encryptionAtHost == nil || !*encryptionAtHost {
		return nil
	}
	return &armcomputefleet.SecurityProfile{
		EncryptionAtHost: lo.ToPtr(true),
	}
}

// extensionsToProfile converts armcompute VM extensions to the armcomputefleet VMSS extension profile format.
func extensionsToProfile(extensions []*armcompute.VirtualMachineExtension) *armcomputefleet.VirtualMachineScaleSetExtensionProfile {
	if len(extensions) == 0 {
		return nil
	}
	fleetExtensions := make([]*armcomputefleet.VirtualMachineScaleSetExtension, 0, len(extensions))
	for _, ext := range extensions {
		if ext == nil {
			continue
		}
		fleetExtensions = append(fleetExtensions, ConvertToScaleSetExtension(ext))
	}
	if len(fleetExtensions) == 0 {
		return nil
	}
	return &armcomputefleet.VirtualMachineScaleSetExtensionProfile{Extensions: fleetExtensions}
}

// ConvertToScaleSetExtension converts armcompute.VirtualMachineExtension to armcomputefleet format.
func ConvertToScaleSetExtension(ext *armcompute.VirtualMachineExtension) *armcomputefleet.VirtualMachineScaleSetExtension {
	if ext == nil || ext.Properties == nil {
		return &armcomputefleet.VirtualMachineScaleSetExtension{}
	}
	extProps := ext.Properties

	return &armcomputefleet.VirtualMachineScaleSetExtension{
		Name: ext.Name,
		Properties: &armcomputefleet.VirtualMachineScaleSetExtensionProperties{
			Publisher:               extProps.Publisher,
			Type:                    extProps.Type,
			TypeHandlerVersion:      extProps.TypeHandlerVersion,
			AutoUpgradeMinorVersion: extProps.AutoUpgradeMinorVersion,
			Settings:                toMapStringAny(extProps.Settings),
			ProtectedSettings:       toMapStringAny(extProps.ProtectedSettings),
		},
	}
}

// toMapStringAny extracts map[string]any from armcompute Settings/ProtectedSettings.
func toMapStringAny(v any) map[string]any {
	if v == nil {
		return nil
	}
	switch m := v.(type) {
	case map[string]any:
		return m
	case *map[string]interface{}:
		if m == nil {
			return nil
		}
		return *m
	default:
		return nil
	}
}

// buildPoolRefs converts load balancer backend pool IDs to SubResource references.
func buildPoolRefs(lbBackendPoolIDs []string) []*armcomputefleet.SubResource {
	if len(lbBackendPoolIDs) == 0 {
		return nil
	}
	sortedPools := slices.Clone(lbBackendPoolIDs)
	slices.Sort(sortedPools)

	poolRefs := make([]*armcomputefleet.SubResource, 0, len(sortedPools))
	for _, lbPoolID := range sortedPools {
		poolRefs = append(poolRefs, &armcomputefleet.SubResource{ID: lo.ToPtr(lbPoolID)})
	}
	return poolRefs
}
