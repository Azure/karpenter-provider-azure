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
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/Azure/skewer"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

const (
	vmNamePrefix       = "aks-"
	computerNamePrefix = "aks-"
	nicConfigName      = "nic"
	sshKeyPathTemplate = "/home/%s/.ssh/authorized_keys"
)

// BuildFleetBody constructs the armcomputefleet.Fleet body from a provision request.
// Slices (SKUs, zones) are sorted internally for deterministic JSON serialization.
func BuildFleetBody(req *FleetVMProvisionRequest, targetCapacity int32, tags map[string]*string) (*armcomputefleet.Fleet, error) {
	if req == nil || req.LaunchTemplate == nil {
		return nil, fmt.Errorf("building Fleet body: request and launch template are required")
	}
	if err := validateFleetRequest(req, targetCapacity); err != nil {
		return nil, fmt.Errorf("building Fleet body: %w", err)
	}
	if len(req.AcceptableSKUs) == 0 {
		return nil, fmt.Errorf("building Fleet body: no candidate SKUs")
	}
	armZones, err := buildZones(req.AcceptableZones)
	if err != nil {
		return nil, fmt.Errorf("building Fleet body: %w", err)
	}
	req, err = filterSKUsForEncryptionAtHost(req)
	if err != nil {
		return nil, fmt.Errorf("building Fleet body: %w", err)
	}

	fleet := &armcomputefleet.Fleet{
		Location:   lo.ToPtr(req.Location),
		Tags:       tags,
		Zones:      armZones,
		Identity:   buildIdentity(req.NodeIdentities),
		Properties: buildFleetProperties(req, req.LaunchTemplate, targetCapacity),
	}
	return fleet, nil
}

func validateFleetRequest(req *FleetVMProvisionRequest, targetCapacity int32) error {
	if targetCapacity <= 0 {
		return fmt.Errorf("target capacity must be positive")
	}
	if req.Location == "" {
		return fmt.Errorf("location is required")
	}
	if err := validateFleetComputeProfile(req); err != nil {
		return err
	}
	if req.NetworkPlugin == consts.NetworkPluginAzure && req.NetworkPluginMode != consts.NetworkPluginModeOverlay &&
		(req.MaxPods < 1 || req.MaxPods > consts.DefaultOverlayMaxPods) {
		return fmt.Errorf("max pods must be between 1 and %d for Azure node subnet networking", consts.DefaultOverlayMaxPods)
	}
	return nil
}

func validateFleetComputeProfile(req *FleetVMProvisionRequest) error {
	if req.LaunchTemplate.ImageID == "" {
		return fmt.Errorf("launch template image ID is required")
	}
	if req.LaunchTemplate.SubnetID == "" {
		return fmt.Errorf("launch template subnet ID is required")
	}
	if req.AdminUsername == "" || req.SSHPublicKey == "" {
		return fmt.Errorf("linux admin username and SSH public key are required")
	}
	if req.CapacityType != karpv1.CapacityTypeSpot && req.CapacityType != karpv1.CapacityTypeOnDemand {
		return fmt.Errorf("unsupported capacity type %q", req.CapacityType)
	}
	if req.LaunchTemplate.IsWindows {
		return fmt.Errorf("windows launch templates are not supported by Fleet")
	}
	if req.LaunchTemplate.EnableFIPS1403Encryption {
		return fmt.Errorf("fleet SDK does not support FIPS 140-3 encryption")
	}
	return nil
}

func filterSKUsForEncryptionAtHost(req *FleetVMProvisionRequest) (*FleetVMProvisionRequest, error) {
	if !lo.FromPtr(req.LaunchTemplate.EncryptionAtHost) {
		return req, nil
	}
	supportedSKUs := lo.Filter(req.AcceptableSKUs, func(name string, _ int) bool {
		sku := req.ResolvedSKUs[name]
		if sku != nil && sku.IsEncryptionAtHostSupported() {
			return true
		}
		log.Log.Info("excluding Fleet candidate SKU because requested encryption at host is not supported", "sku", name)
		return false
	})
	if len(supportedSKUs) == 0 {
		return nil, fmt.Errorf("no candidate SKUs support requested encryption at host")
	}

	filteredReq := *req
	filteredReq.AcceptableSKUs = supportedSKUs
	return &filteredReq, nil
}

// buildZones converts AKS zone labels to ARM zone IDs and sorts them deterministically.
func buildZones(acceptableZones []string) ([]*string, error) {
	if len(acceptableZones) == 0 {
		return nil, nil
	}
	zoneIDs := make(map[string]struct{}, len(acceptableZones))
	hasRegional, hasZonal := false, false
	for _, zone := range acceptableZones {
		if zone == zones.Regional {
			hasRegional = true
			continue
		}
		armZone, err := fleetARMZone(zone)
		if err != nil {
			return nil, err
		}
		zoneIDs[armZone] = struct{}{}
		hasZonal = true
	}
	if hasRegional && hasZonal {
		return nil, fmt.Errorf("regional and zonal offerings cannot share one Fleet request")
	}
	if hasRegional {
		return nil, nil
	}
	sortedZones := make([]string, 0, len(zoneIDs))
	for zoneID := range zoneIDs {
		sortedZones = append(sortedZones, zoneID)
	}
	slices.Sort(sortedZones)

	zoneRefs := make([]*string, 0, len(sortedZones))
	for _, zone := range sortedZones {
		zoneRefs = append(zoneRefs, lo.ToPtr(zone))
	}
	return zoneRefs, nil
}

func fleetARMZone(zone string) (string, error) {
	lastSeparator := strings.LastIndexByte(zone, '-')
	zoneID := zone
	if lastSeparator >= 0 {
		zoneID = zone[lastSeparator+1:]
	}
	parsedZoneID, err := strconv.Atoi(zoneID)
	if err != nil || len(zoneID) != 1 || parsedZoneID < 1 {
		return "", fmt.Errorf("invalid AKS zone label %q", zone)
	}
	return lo.FromPtr(zones.MakeARMZonesFromAKSLabelZone(zone)[0]), nil
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

// buildComputeProfile constructs the Fleet compute profile.
func buildComputeProfile(req *FleetVMProvisionRequest, lt *launchtemplate.Template) *armcomputefleet.ComputeProfile {
	baseProfile := &armcomputefleet.BaseVirtualMachineProfile{
		OSProfile:        buildOSProfile(req, lt),
		StorageProfile:   buildStorageProfile(lt, req.DiskEncryptionSetID),
		NetworkProfile:   BuildFleetNetworkProfile(lt.SubnetID, req.NSG, req.LBBackendPools, allSKUsSupportCapability(req, skewer.AcceleratedNetworking), req.NetworkPlugin, req.NetworkPluginMode, req.MaxPods),
		SecurityProfile:  buildSecurityProfile(lt.EncryptionAtHost, req),
		ExtensionProfile: extensionsToProfile(req.Extensions),
	}
	if req.CapacityReservationGroupID != "" {
		baseProfile.CapacityReservation = &armcomputefleet.CapacityReservationProfile{
			CapacityReservationGroup: &armcomputefleet.SubResource{
				ID: lo.ToPtr(req.CapacityReservationGroupID),
			},
		}
	}

	computeProfile := &armcomputefleet.ComputeProfile{
		BaseVirtualMachineProfile: baseProfile,
	}
	if req.UltraSSDEnabled {
		computeProfile.AdditionalVirtualMachineCapabilities = &armcomputefleet.AdditionalCapabilities{
			UltraSSDEnabled: lo.ToPtr(true),
		}
	}
	return computeProfile
}

// A shared Fleet profile can enable a capability only when every candidate supports it.
func allSKUsSupportCapability(req *FleetVMProvisionRequest, capability string) bool {
	if len(req.AcceptableSKUs) == 0 {
		log.Log.Info("disabling Fleet capability because there are no candidate SKUs", "capability", capability)
		return false
	}
	for _, name := range req.AcceptableSKUs {
		sku := req.ResolvedSKUs[name]
		if sku == nil {
			log.Log.Info("disabling Fleet capability because candidate SKU data is missing", "sku", name, "capability", capability)
			return false
		}
		if !sku.HasCapability(capability) {
			log.Log.Info("disabling Fleet capability because a candidate SKU does not support it", "sku", name, "capability", capability)
			return false
		}
	}
	return true
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
		CommunityGalleryImageID: lo.ToPtr(lt.ImageID),
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

// BuildFleetNetworkProfile constructs the VMSS network profile with subnet, NSG, and primary-only LB backend pools.
func BuildFleetNetworkProfile(subnetID, nsgID string, lbBackendPools []string, enableAcceleratedNetworking bool, networkPlugin, networkPluginMode string, maxPods int32) *armcomputefleet.VirtualMachineScaleSetNetworkProfile {
	ipConfigurationCount := int32(1)
	if networkPlugin == consts.NetworkPluginAzure && networkPluginMode != consts.NetworkPluginModeOverlay {
		// always create a primary IP, then add secondary IPs up to MaxPods.
		ipConfigurationCount = max(1, maxPods)
	}
	ipConfigurations := make([]*armcomputefleet.VirtualMachineScaleSetIPConfiguration, 0, ipConfigurationCount)
	for i := int32(0); i < ipConfigurationCount; i++ {
		properties := &armcomputefleet.VirtualMachineScaleSetIPConfigurationProperties{
			Primary: lo.ToPtr(i == 0),
			Subnet:  &armcomputefleet.APIEntityReference{ID: lo.ToPtr(subnetID)},
		}
		if i == 0 {
			properties.LoadBalancerBackendAddressPools = buildPoolRefs(lbBackendPools)
		}
		ipConfigurations = append(ipConfigurations, &armcomputefleet.VirtualMachineScaleSetIPConfiguration{
			Name:       lo.ToPtr(fmt.Sprintf("ipconfig%d", i+1)),
			Properties: properties,
		})
	}
	nicProperties := &armcomputefleet.VirtualMachineScaleSetNetworkConfigurationProperties{
		Primary:                     lo.ToPtr(true),
		EnableAcceleratedNetworking: lo.ToPtr(enableAcceleratedNetworking),
		EnableIPForwarding:          lo.ToPtr(false),
		DeleteOption:                lo.ToPtr(armcomputefleet.DeleteOptionsDelete),
		IPConfigurations:            ipConfigurations,
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

// buildSecurityProfile maps encryption-at-host and Trusted Launch settings.
func buildSecurityProfile(encryptionAtHost *bool, req *FleetVMProvisionRequest) *armcomputefleet.SecurityProfile {
	if encryptionAtHost == nil && !req.TrustedLaunchEnabled {
		return nil
	}
	profile := &armcomputefleet.SecurityProfile{
		EncryptionAtHost: encryptionAtHost,
	}
	if req.TrustedLaunchEnabled {
		profile.SecurityType = lo.ToPtr(armcomputefleet.SecurityTypesTrustedLaunch)
		profile.UefiSettings = &armcomputefleet.UefiSettings{
			SecureBootEnabled: lo.ToPtr(req.SecureBootEnabled),
			VTpmEnabled:       lo.ToPtr(req.VTPMEnabled),
		}
	}
	return profile
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
