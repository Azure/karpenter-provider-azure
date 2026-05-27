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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

// BuildFleetBody constructs the armcomputefleet.Fleet resource body for a BeginCreateOrUpdate call.
//
// The caller (executor) is responsible for:
//   - Populating `tags` with the batch-key markers (karpenter.azure.com_managed-by, karpenter.azure.com_batch-key-hash)
//   - Passing the fleet name separately in the URL path (Fleet.Name is read-only and never set here)
//
// Parameters:
//   - fields: the BatchKeyFields that define this batch (all requests in the batch share identical fields)
//   - targetCapacity: number of VMs to request from Fleet
//   - tags: pre-merged tag map including batch markers
//   - spotMaxPrice: max price per VM for spot; nil means default (-1 = up to on-demand price)
//   - location: Azure region
//   - lbBackendPools: load balancer backend address pool resource IDs
//   - instanceTypes: map[skuName] → *InstanceType for accelerated networking compatibility check
//   - useSIG: true → use SharedImageGallery ID format; false → use CommunityGalleryImageID
//   - extensions: CSE and billing extensions (nil until fleet-poc-mh-extensions-in-body lands)
func BuildFleetBody(
	fields BatchKeyFields,
	targetCapacity int32,
	tags map[string]*string,
	spotMaxPrice *float32,
	location string,
	lbBackendPools []string,
	instanceTypes map[string]*corecloudprovider.InstanceType,
	useSIG bool,
	extensions []*armcompute.VirtualMachineExtension,
) *armcomputefleet.Fleet {

	fleet := &armcomputefleet.Fleet{
		Location:   lo.ToPtr(location),
		Tags:       tags,
		Zones:      buildZones(fields.Zones),
		Identity:   buildIdentity(fields.NodeIdentities),
		Properties: buildFleetProperties(fields, targetCapacity, spotMaxPrice, lbBackendPools, instanceTypes, useSIG, extensions),
	}

	return fleet
}

// buildZones converts sorted zone strings to the []*string shape required by the SDK.
// Returns nil for an empty/nil zone slice (regional Fleet — no zone pinning).
func buildZones(zones []string) []*string {
	if len(zones) == 0 {
		return nil
	}
	return lo.Map(zones, func(z string, _ int) *string {
		return lo.ToPtr(z)
	})
}

// buildIdentity constructs the ManagedServiceIdentity from the comma-joined NodeIdentities string.
// Returns nil if no identities are configured — Fleet will use system-assigned by default.
func buildIdentity(joined string) *armcomputefleet.ManagedServiceIdentity {
	if joined == "" {
		return nil
	}
	ids := strings.Split(joined, ",")
	m := make(map[string]*armcomputefleet.UserAssignedIdentity, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		m[id] = &armcomputefleet.UserAssignedIdentity{}
	}
	if len(m) == 0 {
		return nil
	}
	return &armcomputefleet.ManagedServiceIdentity{
		Type:                   lo.ToPtr(armcomputefleet.ManagedServiceIdentityTypeUserAssigned),
		UserAssignedIdentities: m,
	}
}

// buildFleetProperties assembles the core FleetProperties with the appropriate priority profile.
func buildFleetProperties(
	fields BatchKeyFields,
	targetCapacity int32,
	spotMaxPrice *float32,
	lbBackendPools []string,
	instanceTypes map[string]*corecloudprovider.InstanceType,
	useSIG bool,
	extensions []*armcompute.VirtualMachineExtension,
) *armcomputefleet.FleetProperties {
	props := &armcomputefleet.FleetProperties{
		VMSizesProfile: buildVMSizesProfile(fields.CandidateSKUs),
		ComputeProfile: buildComputeProfile(fields, lbBackendPools, instanceTypes, useSIG, extensions),
	}

	switch fields.CapacityType {
	case karpv1.CapacityTypeSpot:
		props.SpotPriorityProfile = buildSpotProfile(targetCapacity, spotMaxPrice)
	case karpv1.CapacityTypeOnDemand:
		props.RegularPriorityProfile = buildRegularProfile(targetCapacity)
	default:
		// Unreachable in POC — batch key has already separated by capacity type.
		// Fall through to regular as a defensive default; a panic here would crash
		// the batcher goroutine and tank the whole batch.
		props.RegularPriorityProfile = buildRegularProfile(targetCapacity)
	}

	return props
}

// buildVMSizesProfile creates one VMSizeProfile entry per candidate SKU.
// Rank is intentionally not set — deferred to fleet-poc-vmsize-rank (§15.2.9).
func buildVMSizesProfile(skus []string) []*armcomputefleet.VMSizeProfile {
	out := make([]*armcomputefleet.VMSizeProfile, 0, len(skus))
	for _, s := range skus {
		out = append(out, &armcomputefleet.VMSizeProfile{Name: lo.ToPtr(s)})
	}
	return out
}

// buildSpotProfile constructs the spot priority profile.
// maintain=false is non-negotiable — Karpenter manages eviction/replacement, not Fleet.
func buildSpotProfile(capacity int32, maxPrice *float32) *armcomputefleet.SpotPriorityProfile {
	if maxPrice == nil {
		// Default to -1: willing to pay up to on-demand price, matching VM-path behaviour.
		maxPrice = lo.ToPtr(float32(-1))
	}
	return &armcomputefleet.SpotPriorityProfile{
		Capacity:           lo.ToPtr(int32(capacity)),
		AllocationStrategy: lo.ToPtr(armcomputefleet.SpotAllocationStrategyPriceCapacityOptimized),
		EvictionPolicy:     lo.ToPtr(armcomputefleet.EvictionPolicyDelete),
		Maintain:           lo.ToPtr(false),
		MaxPricePerVM:      maxPrice,
	}
}

// buildRegularProfile constructs the on-demand (regular) priority profile.
func buildRegularProfile(capacity int32) *armcomputefleet.RegularPriorityProfile {
	return &armcomputefleet.RegularPriorityProfile{
		Capacity:           lo.ToPtr(int32(capacity)),
		AllocationStrategy: lo.ToPtr(armcomputefleet.RegularPriorityAllocationStrategyLowestPrice),
	}
}

// buildComputeProfile constructs the BaseVirtualMachineProfile containing OS, storage,
// network, security, and extension profiles.
func buildComputeProfile(
	fields BatchKeyFields,
	lbBackendPools []string,
	instanceTypes map[string]*corecloudprovider.InstanceType,
	useSIG bool,
	extensions []*armcompute.VirtualMachineExtension,
) *armcomputefleet.ComputeProfile {
	enableAN := acceleratedNetworkingForAll(fields.CandidateSKUs, instanceTypes)

	baseProfile := &armcomputefleet.BaseVirtualMachineProfile{
		OSProfile:        buildOSProfile(fields),
		StorageProfile:   buildStorageProfile(fields, useSIG),
		NetworkProfile:   buildNetworkProfile(fields.SubnetID, fields.NSG, lbBackendPools, enableAN),
		SecurityProfile:  buildSecurityProfile(fields.EncryptionAtHost),
		ExtensionProfile: extensionsToProfile(extensions),
	}

	return &armcomputefleet.ComputeProfile{
		BaseVirtualMachineProfile: baseProfile,
	}
}

// buildOSProfile constructs the Linux OS profile with SSH key and custom data.
// Windows is not supported in POC — returns nil if fields indicate Windows (currently impossible).
func buildOSProfile(fields BatchKeyFields) *armcomputefleet.VirtualMachineScaleSetOSProfile {
	sshPath := "/home/" + fields.AdminUsername + "/.ssh/authorized_keys"

	profile := &armcomputefleet.VirtualMachineScaleSetOSProfile{
		AdminUsername: lo.ToPtr(fields.AdminUsername),
		// TODO(fleet-poc-mh-executor): ComputerNamePrefix format TBD; using "aks-" for POC.
		ComputerNamePrefix: lo.ToPtr("aks-"),
		LinuxConfiguration: &armcomputefleet.LinuxConfiguration{
			DisablePasswordAuthentication: lo.ToPtr(true),
			SSH: &armcomputefleet.SSHConfiguration{
				PublicKeys: []*armcomputefleet.SSHPublicKey{{
					KeyData: lo.ToPtr(fields.SSHPublicKey),
					Path:    lo.ToPtr(sshPath),
				}},
			},
		},
	}
	if fields.CustomData != "" {
		profile.CustomData = lo.ToPtr(fields.CustomData)
	}
	return profile
}

// buildStorageProfile constructs the OS disk and image reference.
func buildStorageProfile(fields BatchKeyFields, useSIG bool) *armcomputefleet.VirtualMachineScaleSetStorageProfile {
	// Image reference — SIG vs Community Gallery
	imageRef := &armcomputefleet.ImageReference{}
	if useSIG {
		imageRef.ID = lo.ToPtr(fields.ImageID)
	} else {
		imageRef.CommunityGalleryImageID = lo.ToPtr(fields.ImageID)
	}

	osDisk := &armcomputefleet.VirtualMachineScaleSetOSDisk{
		CreateOption: lo.ToPtr(armcomputefleet.DiskCreateOptionTypesFromImage),
		DiskSizeGB:   lo.ToPtr(int32(fields.OSDiskSizeGB)),
		OSType:       lo.ToPtr(armcomputefleet.OperatingSystemTypesLinux),
	}

	// Ephemeral disk settings
	if fields.OSDiskType != "" {
		osDisk.DiffDiskSettings = &armcomputefleet.DiffDiskSettings{
			Option:    lo.ToPtr(armcomputefleet.DiffDiskOptionsLocal),
			Placement: lo.ToPtr(armcomputefleet.DiffDiskPlacement(fields.OSDiskType)),
		}
		osDisk.Caching = lo.ToPtr(armcomputefleet.CachingTypesReadOnly)
	}

	// Disk encryption set
	if fields.DiskEncryptionSetID != "" {
		osDisk.ManagedDisk = &armcomputefleet.VirtualMachineScaleSetManagedDiskParameters{
			DiskEncryptionSet: &armcomputefleet.DiskEncryptionSetParameters{
				ID: lo.ToPtr(fields.DiskEncryptionSetID),
			},
		}
	}

	return &armcomputefleet.VirtualMachineScaleSetStorageProfile{
		ImageReference: imageRef,
		OSDisk:         osDisk,
	}
}

// buildNetworkProfile constructs the VMSS network profile with subnet, NSG, accelerated
// networking, and LB backend pools.
//
// TODO(fleet-poc-mh-secondary-ips): Secondary IP configurations for AzureCNI-without-overlay
// are deferred. POC assumes overlay or non-Azure-CNI networking.
func buildNetworkProfile(subnetID, nsgID string, lbBackendPools []string, enableAcceleratedNetworking bool) *armcomputefleet.VirtualMachineScaleSetNetworkProfile {
	nicProps := &armcomputefleet.VirtualMachineScaleSetNetworkConfigurationProperties{
		Primary:                     lo.ToPtr(true),
		EnableAcceleratedNetworking: lo.ToPtr(enableAcceleratedNetworking),
		EnableIPForwarding:          lo.ToPtr(false),
		DeleteOption:                lo.ToPtr(armcomputefleet.DeleteOptionsDelete),
		IPConfigurations: []*armcomputefleet.VirtualMachineScaleSetIPConfiguration{{
			Name: lo.ToPtr("ipconfig1"),
			Properties: &armcomputefleet.VirtualMachineScaleSetIPConfigurationProperties{
				Primary: lo.ToPtr(true),
				Subnet:  &armcomputefleet.APIEntityReference{ID: lo.ToPtr(subnetID)},
				LoadBalancerBackendAddressPools: buildPoolRefs(lbBackendPools),
			},
		}},
	}
	if nsgID != "" {
		nicProps.NetworkSecurityGroup = &armcomputefleet.SubResource{ID: lo.ToPtr(nsgID)}
	}
	return &armcomputefleet.VirtualMachineScaleSetNetworkProfile{
		NetworkInterfaceConfigurations: []*armcomputefleet.VirtualMachineScaleSetNetworkConfiguration{{
			Name:       lo.ToPtr("nic"),
			Properties: nicProps,
		}},
	}
}

// buildSecurityProfile returns the security profile only when encryption at host is enabled.
// When false, returns nil — matching the VM path which only sets it when the helper returns true.
func buildSecurityProfile(encryptionAtHost bool) *armcomputefleet.SecurityProfile {
	if !encryptionAtHost {
		return nil
	}
	return &armcomputefleet.SecurityProfile{
		EncryptionAtHost: lo.ToPtr(true),
	}
}

// extensionsToProfile converts armcompute VM extensions to the armcomputefleet VMSS extension
// profile format. Returns nil when no extensions are provided — allows this item to compile
// and test independently of fleet-poc-mh-extensions-in-body (§15.1.3).
//
// TODO(fleet-poc-mh-extensions-in-body): Implement ConvertToScaleSetExtension and wire here.
func extensionsToProfile(exts []*armcompute.VirtualMachineExtension) *armcomputefleet.VirtualMachineScaleSetExtensionProfile {
	if len(exts) == 0 {
		return nil
	}
	converted := make([]*armcomputefleet.VirtualMachineScaleSetExtension, 0, len(exts))
	for _, e := range exts {
		if e == nil {
			continue
		}
		converted = append(converted, convertToScaleSetExtension(e))
	}
	if len(converted) == 0 {
		return nil
	}
	return &armcomputefleet.VirtualMachineScaleSetExtensionProfile{Extensions: converted}
}

// convertToScaleSetExtension converts a single armcompute.VirtualMachineExtension to the
// armcomputefleet.VirtualMachineScaleSetExtension format.
func convertToScaleSetExtension(ext *armcompute.VirtualMachineExtension) *armcomputefleet.VirtualMachineScaleSetExtension {
	if ext == nil || ext.Properties == nil {
		return &armcomputefleet.VirtualMachineScaleSetExtension{}
	}
	props := ext.Properties

	return &armcomputefleet.VirtualMachineScaleSetExtension{
		Name: ext.Name,
		Properties: &armcomputefleet.VirtualMachineScaleSetExtensionProperties{
			Publisher:               props.Publisher,
			Type:                    props.Type,
			TypeHandlerVersion:      props.TypeHandlerVersion,
			AutoUpgradeMinorVersion: props.AutoUpgradeMinorVersion,
			Settings:                toMapStringAny(props.Settings),
			ProtectedSettings:       toMapStringAny(props.ProtectedSettings),
		},
	}
}

// toMapStringAny extracts a map[string]any from the armcompute Settings/ProtectedSettings field.
// The SDK declares these as `any`; the actual runtime value may be:
//   - map[string]any — direct JSON unmarshalling
//   - *map[string]interface{} — the pattern used in extension constructors (getCSExtension, etc.)
//   - nil — no settings
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
func buildPoolRefs(pools []string) []*armcomputefleet.SubResource {
	if len(pools) == 0 {
		return nil
	}
	out := make([]*armcomputefleet.SubResource, 0, len(pools))
	for _, id := range pools {
		out = append(out, &armcomputefleet.SubResource{ID: lo.ToPtr(id)})
	}
	return out
}

// acceleratedNetworkingForAll implements the "conservative AND" policy:
// only enable accelerated networking if EVERY candidate SKU supports it.
// If any SKU in the candidate set doesn't support it, Fleet would fail to create VMs
// of that SKU when accelerated networking is true on the NIC profile.
//
// This generalizes the VM path's per-SKU check (vminstance.go:460) across the candidate set.
func acceleratedNetworkingForAll(skus []string, instanceTypes map[string]*corecloudprovider.InstanceType) bool {
	if len(skus) == 0 {
		return false
	}
	anRequirements := scheduling.NewRequirements(
		scheduling.NewRequirement(v1beta1.LabelSKUAcceleratedNetworking, corev1.NodeSelectorOpIn, "true"),
	)
	for _, sku := range skus {
		it, ok := instanceTypes[sku]
		if !ok {
			return false
		}
		if err := it.Requirements.Compatible(anRequirements); err != nil {
			return false
		}
	}
	return true
}
