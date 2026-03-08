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
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	labelpkg "github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// resolvedBootstrapData carries the output of resolveBootstrapAndImageData.
type resolvedBootstrapData struct {
	ScriptlessCustomData    string
	CustomScriptsCustomData string
	CustomScriptsCSE        string
	ImageID                 string
	SubnetID                string
	Tags                    map[string]*string
	IsWindows               bool
	StorageProfileDiskType  string
	IsEphemeral             bool
	EphemeralPlacement      armcompute.DiffDiskPlacement
	StorageProfileSizeGB    int32
}

// resolveBootstrapAndImageData replaces the launchtemplate.Provider.GetTemplate chain.
// It builds StaticParameters locally, calls imagefamily.Resolve(), renders bootstrap data, and returns the result.
// In AzureVM mode, it bypasses image family resolution entirely and uses user-provided imageID/userData.
func (p *DefaultVMProvider) resolveBootstrapAndImageData(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
) (*resolvedBootstrapData, error) {
	// In AzureVM mode, bypass image family resolution entirely.
	// The user provides their own imageID and userData via AzureNodeClass.
	if p.provisionMode == consts.ProvisionModeAzureVM {
		return p.resolveAzureVMBootstrapData(ctx, nodeClass, nodeClaim)
	}

	// Build additional labels (same logic as old getLaunchTemplate)
	claimLabels := labelpkg.GetFilteredSingleValuedRequirementLabels(
		scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...),
		func(k string, req *scheduling.Requirement) bool {
			return labelpkg.IsKubeletLabel(k)
		},
	)
	additionalLabels := lo.Assign(
		claimLabels,
		labelpkg.GetAllSingleValuedRequirementLabels(instanceType.Requirements),
		map[string]string{karpv1.CapacityTypeLabelKey: capacityType},
	)

	// Build StaticParameters (previously in launchtemplate.Provider.getStaticParameters)
	opts := options.FromContext(ctx)
	var arch = karpv1.ArchitectureAmd64
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64))); err == nil {
		arch = karpv1.ArchitectureArm64
	}

	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), opts.SubnetID)
	baseLabels, err := labelpkg.Get(ctx, nodeClass)
	if err != nil {
		return nil, err
	}
	labels := lo.Assign(baseLabels, lo.Assign(nodeClaim.Labels, additionalLabels))

	staticParams := &parameters.StaticParameters{
		ClusterName:                    opts.ClusterName,
		ClusterEndpoint:                p.clusterEndpoint,
		Labels:                         labels,
		CABundle:                       p.caBundle,
		Arch:                           arch,
		GPUNode:                        utils.IsNvidiaEnabledSKU(instanceType.Name),
		GPUDriverVersion:               utils.GetGPUDriverVersion(instanceType.Name),
		GPUDriverType:                  utils.GetGPUDriverType(instanceType.Name),
		GPUImageSHA:                    utils.GetAKSGPUImageSHA(instanceType.Name),
		TenantID:                       p.tenantID,
		SubscriptionID:                 p.subscriptionID,
		KubeletIdentityClientID:        opts.KubeletIdentityClientID,
		ResourceGroup:                  p.resourceGroup,
		Location:                       p.location,
		ClusterID:                      opts.ClusterID,
		APIServerName:                  opts.GetAPIServerName(),
		KubeletClientTLSBootstrapToken: opts.KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  getAgentbakerNetworkPlugin(ctx),
		NetworkPolicy:                  opts.NetworkPolicy,
		SubnetID:                       subnetID,
		ClusterResourceGroup:           p.clusterResourceGroup,
	}

	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, err
	}
	staticParams.KubernetesVersion = kubernetesVersion

	// Resolve image and bootstrap via imagefamily
	templateParams, err := p.imageResolver.Resolve(ctx, nodeClass, nodeClaim, instanceType, staticParams)
	if err != nil {
		return nil, err
	}

	// Render bootstrap data (previously in launchtemplate.Provider.createLaunchTemplate)
	result := &resolvedBootstrapData{
		ImageID:                templateParams.ImageID,
		SubnetID:               templateParams.SubnetID,
		IsWindows:              templateParams.IsWindows,
		StorageProfileDiskType: templateParams.StorageProfileDiskType,
		IsEphemeral:            templateParams.StorageProfileIsEphemeral,
		EphemeralPlacement:     templateParams.StorageProfilePlacement,
		StorageProfileSizeGB:   templateParams.StorageProfileSizeGB,
	}

	switch p.provisionMode {
	case consts.ProvisionModeBootstrappingClient:
		customData, cse, err := templateParams.CustomScriptsNodeBootstrapping.GetCustomDataAndCSE(ctx)
		if err != nil {
			return nil, err
		}
		result.CustomScriptsCustomData = customData
		result.CustomScriptsCSE = cse
	case consts.ProvisionModeAKSScriptless:
		userData, err := templateParams.ScriptlessCustomData.Script()
		if err != nil {
			return nil, err
		}
		result.ScriptlessCustomData = userData
	}

	result.Tags = launchtemplate.Tags(opts, nodeClass, nodeClaim)

	return result, nil
}

// resolveAzureVMBootstrapData creates minimal bootstrap data for AzureVM mode.
// In this mode, no Karpenter-managed bootstrapping or image resolution is performed.
// The user provides imageID and userData directly via AzureNodeClass.
func (p *DefaultVMProvider) resolveAzureVMBootstrapData(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
) (*resolvedBootstrapData, error) {
	opts := options.FromContext(ctx)
	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), opts.SubnetID)

	imageID := ""
	if nodeClass.Spec.ImageID != nil {
		imageID = *nodeClass.Spec.ImageID
	}
	if imageID == "" {
		return nil, fmt.Errorf("imageID is required in AzureVM mode")
	}

	return &resolvedBootstrapData{
		ImageID:  imageID,
		SubnetID: subnetID,
		Tags:     launchtemplate.Tags(opts, nodeClass, nodeClaim),
	}, nil
}

func getAgentbakerNetworkPlugin(ctx context.Context) string {
	opts := options.FromContext(ctx)
	if opts.IsAzureCNIOverlay() || opts.IsCiliumNodeSubnet() || opts.IsNetworkPluginNone() {
		return consts.NetworkPluginNone
	}
	return consts.NetworkPluginAzure
}

// buildVMTemplate builds an armcompute.VirtualMachine from the resolved bootstrap data.
func (p *DefaultVMProvider) buildVMTemplate(
	ctx context.Context,
	instanceType *corecloudprovider.InstanceType,
	capacityType, zone string,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	nicReference string,
) (*armcompute.VirtualMachine, *resolvedBootstrapData, error) {
	bootstrap, err := p.resolveBootstrapAndImageData(ctx, nodeClass, nodeClaim, instanceType, capacityType)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving bootstrap and image data: %w", err)
	}

	if bootstrap.IsWindows {
		return &armcompute.VirtualMachine{}, bootstrap, nil // TODO(Windows)
	}

	opts := options.FromContext(ctx)
	vmName := GenerateResourceName(nodeClaim.Name)

	vm := &armcompute.VirtualMachine{
		Name:     lo.ToPtr(vmName),
		Location: lo.ToPtr(p.location),
		Identity: buildVMIdentity(opts.NodeIdentities, nodeClass),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypes(instanceType.Name)),
			},
			StorageProfile: configureStorageProfile(bootstrap, nodeClass, p.diskEncryptionSetID, opts.UseSIG, p.provisionMode, vmName),
			NetworkProfile: configureNetworkProfile(nicReference),
			OSProfile:      configureOSProfile(opts, vmName, bootstrap, p.provisionMode, nodeClass),
			Priority:       lo.ToPtr(KarpCapacityTypeToVMPriority[capacityType]),
		},
		Zones: utils.MakeARMZonesFromAKSLabelZone(zone),
		Tags:  bootstrap.Tags,
	}

	configureBillingProfile(vm.Properties, capacityType)
	configureSecurityProfile(vm.Properties, nodeClass)
	configureDataDisk(vm.Properties, nodeClass)

	return vm, bootstrap, nil
}

// configureStorageProfile builds the StorageProfile for the VM.
func configureStorageProfile(bootstrap *resolvedBootstrapData, nodeClass *v1beta1.AKSNodeClass, diskEncryptionSetID string, useSIG bool, provisionMode string, vmName string) *armcompute.StorageProfile {
	osDisk := &armcompute.OSDisk{
		Name:         lo.ToPtr(vmName),
		DiskSizeGB:   nodeClass.Spec.OSDiskSizeGB,
		CreateOption: lo.ToPtr(armcompute.DiskCreateOptionTypesFromImage),
		DeleteOption: lo.ToPtr(armcompute.DiskDeleteOptionTypesDelete),
	}

	// Ephemeral disk settings
	if bootstrap.IsEphemeral {
		osDisk.DiffDiskSettings = &armcompute.DiffDiskSettings{
			Option:    lo.ToPtr(armcompute.DiffDiskOptionsLocal),
			Placement: lo.ToPtr(bootstrap.EphemeralPlacement),
		}
		osDisk.Caching = lo.ToPtr(armcompute.CachingTypesReadOnly)
	}

	// Disk encryption
	if diskEncryptionSetID != "" {
		if osDisk.ManagedDisk == nil {
			osDisk.ManagedDisk = &armcompute.ManagedDiskParameters{}
		}
		osDisk.ManagedDisk.DiskEncryptionSet = &armcompute.DiskEncryptionSetParameters{
			ID: lo.ToPtr(diskEncryptionSetID),
		}
	}

	// Image reference
	var imageRef *armcompute.ImageReference
	if provisionMode == consts.ProvisionModeAzureVM {
		// In AzureVM mode, the imageID is always a direct ARM resource ID
		// provided by the user in AzureNodeClass.Spec.ImageID
		imageRef = &armcompute.ImageReference{
			ID: lo.ToPtr(bootstrap.ImageID),
		}
	} else if useSIG {
		imageRef = &armcompute.ImageReference{
			ID: lo.ToPtr(bootstrap.ImageID),
		}
	} else {
		imageRef = &armcompute.ImageReference{
			CommunityGalleryImageID: lo.ToPtr(bootstrap.ImageID),
		}
	}

	return &armcompute.StorageProfile{
		OSDisk:         osDisk,
		ImageReference: imageRef,
	}
}

// configureNetworkProfile builds the NetworkProfile for the VM.
func configureNetworkProfile(nicReference string) *armcompute.NetworkProfile {
	return &armcompute.NetworkProfile{
		NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
			{
				ID: &nicReference,
				Properties: &armcompute.NetworkInterfaceReferenceProperties{
					Primary:      lo.ToPtr(true),
					DeleteOption: lo.ToPtr(armcompute.DeleteOptionsDelete),
				},
			},
		},
	}
}

// configureOSProfile builds the OSProfile for the VM.
func configureOSProfile(opts *options.Options, vmName string, bootstrap *resolvedBootstrapData, provisionMode string, nodeClass *v1beta1.AKSNodeClass) *armcompute.OSProfile {
	osProfile := &armcompute.OSProfile{
		ComputerName: &vmName,
	}

	if provisionMode == consts.ProvisionModeAzureVM {
		// In AzureVM mode, SSH key and admin username are optional.
		// If provided, configure them; otherwise omit SSH configuration entirely.
		if opts.LinuxAdminUsername != "" {
			osProfile.AdminUsername = lo.ToPtr(opts.LinuxAdminUsername)
		}
		if opts.SSHPublicKey != "" && opts.LinuxAdminUsername != "" {
			osProfile.LinuxConfiguration = &armcompute.LinuxConfiguration{
				DisablePasswordAuthentication: lo.ToPtr(true),
				SSH: &armcompute.SSHConfiguration{
					PublicKeys: []*armcompute.SSHPublicKey{
						{
							KeyData: lo.ToPtr(opts.SSHPublicKey),
							Path:    lo.ToPtr("/home/" + opts.LinuxAdminUsername + "/.ssh/authorized_keys"),
						},
					},
				},
			}
		} else {
			osProfile.LinuxConfiguration = &armcompute.LinuxConfiguration{
				DisablePasswordAuthentication: lo.ToPtr(true),
			}
		}
		// In AzureVM mode, pass through pre-base64-encoded userData from the
		// AzureNodeClass adapter directly to osProfile.CustomData. The Azure API
		// expects base64, and the SDK does NOT auto-encode.
		// UserData may be nil/empty — it's the user's responsibility to provide valid bootstrap data.
		if nodeClass.Spec.UserData != nil {
			osProfile.CustomData = nodeClass.Spec.UserData
		}
	} else {
		// AKS modes: SSH key and admin username are required
		osProfile.AdminUsername = lo.ToPtr(opts.LinuxAdminUsername)
		osProfile.LinuxConfiguration = &armcompute.LinuxConfiguration{
			DisablePasswordAuthentication: lo.ToPtr(true),
			SSH: &armcompute.SSHConfiguration{
				PublicKeys: []*armcompute.SSHPublicKey{
					{
						KeyData: lo.ToPtr(opts.SSHPublicKey),
						Path:    lo.ToPtr("/home/" + opts.LinuxAdminUsername + "/.ssh/authorized_keys"),
					},
				},
			},
		}
		if provisionMode == consts.ProvisionModeBootstrappingClient {
			osProfile.CustomData = lo.ToPtr(bootstrap.CustomScriptsCustomData)
		} else {
			osProfile.CustomData = lo.ToPtr(bootstrap.ScriptlessCustomData)
		}
	}

	return osProfile
}

// buildVMIdentity creates the VM identity configuration, merging global node identities
// with any per-NodeClass managed identities (from AzureNodeClass in AzureVM mode).
func buildVMIdentity(nodeIdentities []string, nodeClass *v1beta1.AKSNodeClass) *armcompute.VirtualMachineIdentity {
	allIdentities := nodeIdentities
	if len(nodeClass.Spec.ManagedIdentities) > 0 {
		// Merge nodeclass-level identities with global identities, deduplicating
		seen := make(map[string]bool, len(allIdentities))
		for _, id := range allIdentities {
			seen[id] = true
		}
		for _, id := range nodeClass.Spec.ManagedIdentities {
			if !seen[id] {
				allIdentities = append(allIdentities, id)
				seen[id] = true
			}
		}
	}
	return ConvertToVirtualMachineIdentity(allIdentities)
}

// configureBillingProfile sets a default MaxPrice of -1 for Spot.
func configureBillingProfile(vmProps *armcompute.VirtualMachineProperties, capacityType string) {
	if capacityType == karpv1.CapacityTypeSpot {
		vmProps.EvictionPolicy = lo.ToPtr(armcompute.VirtualMachineEvictionPolicyTypesDelete)
		vmProps.BillingProfile = &armcompute.BillingProfile{
			MaxPrice: lo.ToPtr(float64(-1)),
		}
	}
}

// configureSecurityProfile sets security-related properties.
func configureSecurityProfile(vmProps *armcompute.VirtualMachineProperties, nodeClass *v1beta1.AKSNodeClass) {
	if nodeClass.Spec.Security != nil && nodeClass.Spec.Security.EncryptionAtHost != nil {
		if vmProps.SecurityProfile == nil {
			vmProps.SecurityProfile = &armcompute.SecurityProfile{}
		}
		vmProps.SecurityProfile.EncryptionAtHost = nodeClass.Spec.Security.EncryptionAtHost
	}
}

// configureDataDisk attaches an additional Premium_LRS managed data disk when
// DataDiskSizeGB is set on the NodeClass (via AzureNodeClass adapter).
func configureDataDisk(vmProps *armcompute.VirtualMachineProperties, nodeClass *v1beta1.AKSNodeClass) {
	if nodeClass.Spec.DataDiskSizeGB == nil || *nodeClass.Spec.DataDiskSizeGB <= 0 {
		return
	}
	vmProps.StorageProfile.DataDisks = append(vmProps.StorageProfile.DataDisks, &armcompute.DataDisk{
		Lun:          lo.ToPtr(int32(0)),
		Name:         lo.ToPtr(lo.FromPtr(vmProps.StorageProfile.OSDisk.Name) + "-data-0"),
		DiskSizeGB:   nodeClass.Spec.DataDiskSizeGB,
		CreateOption: lo.ToPtr(armcompute.DiskCreateOptionTypesEmpty),
		DeleteOption: lo.ToPtr(armcompute.DiskDeleteOptionTypesDelete),
		ManagedDisk: &armcompute.ManagedDiskParameters{
			StorageAccountType: lo.ToPtr(armcompute.StorageAccountTypesPremiumLRS),
		},
	})
}

// buildAKSBillingExtensionSpec returns the AKS billing extension spec.
func buildAKSBillingExtensionSpec(location string, tags map[string]*string) *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType                  = "Microsoft.Compute/virtualMachines/extensions"
		aksIdentifyingExtensionPublisher = "Microsoft.AKS"
		aksIdentifyingExtensionTypeLinux = "Compute.AKS.Linux.Billing"
	)

	return &armcompute.VirtualMachineExtension{
		Location: lo.ToPtr(location),
		Name:     lo.ToPtr(aksIdentifyingExtensionName),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			Publisher:               lo.ToPtr(aksIdentifyingExtensionPublisher),
			TypeHandlerVersion:      lo.ToPtr("1.0"),
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Settings:                &map[string]interface{}{},
			Type:                    lo.ToPtr(aksIdentifyingExtensionTypeLinux),
		},
		Type: lo.ToPtr(vmExtensionType),
		Tags: tags,
	}
}

// buildCSExtensionSpec returns the custom script extension spec.
func buildCSExtensionSpec(location, cse string, isWindows bool, tags map[string]*string) *armcompute.VirtualMachineExtension {
	const (
		vmExtensionType     = "Microsoft.Compute/virtualMachines/extensions"
		cseTypeWindows      = "CustomScriptExtension"
		csePublisherWindows = "Microsoft.Compute"
		cseVersionWindows   = "1.10"
		cseTypeLinux        = "CustomScript"
		csePublisherLinux   = "Microsoft.Azure.Extensions"
		cseVersionLinux     = "2.0"
	)

	return &armcompute.VirtualMachineExtension{
		Location: lo.ToPtr(location),
		Name:     lo.ToPtr(lo.Ternary(isWindows, cseNameWindows, cseNameLinux)),
		Type:     lo.ToPtr(vmExtensionType),
		Properties: &armcompute.VirtualMachineExtensionProperties{
			AutoUpgradeMinorVersion: lo.ToPtr(true),
			Type:                    lo.ToPtr(lo.Ternary(isWindows, cseTypeWindows, cseTypeLinux)),
			Publisher:               lo.ToPtr(lo.Ternary(isWindows, csePublisherWindows, csePublisherLinux)),
			TypeHandlerVersion:      lo.ToPtr(lo.Ternary(isWindows, cseVersionWindows, cseVersionLinux)),
			Settings:                &map[string]interface{}{},
			ProtectedSettings: &map[string]interface{}{
				"commandToExecute": cse,
			},
		},
		Tags: tags,
	}
}

// createAKSIdentifyingExtensionFromSpec attaches a VM extension to identify that this VM participates in an AKS cluster
func (p *DefaultVMProvider) createAKSIdentifyingExtensionFromSpec(ctx context.Context, vmName string, tags map[string]*string) error {
	vmExt := buildAKSBillingExtensionSpec(p.location, tags)
	vmExtName := *vmExt.Name
	log.FromContext(ctx).V(1).Info("creating virtual machine AKS identifying extension", "vmName", vmName)
	v, err := createVirtualMachineExtension(ctx, p.azClient.VirtualMachineExtensionsClient(), p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		return fmt.Errorf("creating VM AKS identifying extension %q for VM %q: %w", vmExtName, vmName, err)
	}
	log.FromContext(ctx).V(1).Info("created virtual machine AKS identifying extension",
		"vmName", vmName,
		"extensionID", *v.ID,
	)
	return nil
}

// createCSExtensionFromSpec creates the custom script extension on the VM.
func (p *DefaultVMProvider) createCSExtensionFromSpec(ctx context.Context, vmName string, cse string, isWindows bool, tags map[string]*string) error {
	vmExt := buildCSExtensionSpec(p.location, cse, isWindows, tags)
	vmExtName := *vmExt.Name
	log.FromContext(ctx).V(1).Info("creating virtual machine CSE", "vmName", vmName)
	v, err := createVirtualMachineExtension(ctx, p.azClient.VirtualMachineExtensionsClient(), p.resourceGroup, vmName, vmExtName, *vmExt)
	if err != nil {
		return fmt.Errorf("creating VM CSE for VM %q: %w", vmName, err)
	}
	log.FromContext(ctx).V(1).Info("created virtual machine CSE",
		"vmName", vmName,
		"extensionID", *v.ID,
	)
	return nil
}
