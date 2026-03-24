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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/bootstrap/customscripts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/bootstrap/scriptless"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	labelpkg "github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// resolvedBootstrap carries the rendered bootstrap output needed for VM creation.
type resolvedBootstrap struct {
	CustomData string // base64-encoded cloud-init custom data
	CSE        string // CSE command (only for BootstrappingClient mode)
	IsWindows  bool
}

// resolveImageID resolves the VM image to use for the given nodeClass and instanceType.
func (p *DefaultVMProvider) resolveImageID(nodeClass *v1beta1.AKSNodeClass, instanceType *corecloudprovider.InstanceType) (string, error) {
	imageID, err := p.imageResolver.ResolveNodeImageFromNodeClass(nodeClass, instanceType)
	if err != nil {
		return "", fmt.Errorf("resolving image: %w", err)
	}
	return imageID, nil
}

// resolveSubnetID returns the subnet to use for the VM's NIC.
func resolveSubnetID(nodeClass *v1beta1.AKSNodeClass, opts *options.Options) string {
	return lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), opts.SubnetID)
}

// resolveBootstrap builds bootstrap data (custom data + CSE) for the VM.
// Each parameter flows directly to the bootstrap struct — no intermediate StaticParameters bag.
// Parallel to how aksmachineinstancehelpers.go's buildAKSMachineTemplate builds its template directly.
func (p *DefaultVMProvider) resolveBootstrap(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	imageID string,
) (*resolvedBootstrap, error) {
	opts := options.FromContext(ctx)
	arch := resolveArch(instanceType)
	subnetID := resolveSubnetID(nodeClass, opts)

	labels, err := resolveLabels(ctx, nodeClass, nodeClaim, instanceType, capacityType, arch)
	if err != nil {
		return nil, err
	}

	kubernetesVersion, err := nodeClass.GetKubernetesVersion()
	if err != nil {
		return nil, err
	}

	kubeletConfig := prepareKubeletConfiguration(ctx, instanceType, nodeClass)
	generalTaints, startupTaints := utils.ExtractTaints(nodeClaim)

	result := &resolvedBootstrap{
		IsWindows: false, // TODO(Windows)
	}

	switch p.provisionMode {
	case consts.ProvisionModeAKSScriptless:
		bootstrapper := p.buildScriptlessBootstrapper(opts, instanceType, kubeletConfig, generalTaints, startupTaints, labels, subnetID, arch, kubernetesVersion)
		customData, err := bootstrapper.Script()
		if err != nil {
			return nil, fmt.Errorf("rendering scriptless custom data: %w", err)
		}
		result.CustomData = customData

	case consts.ProvisionModeBootstrappingClient:
		bootstrapper, err := p.buildCustomScriptsBootstrapper(ctx, nodeClass, instanceType, imageID, kubeletConfig, generalTaints, startupTaints, labels, subnetID, arch, kubernetesVersion)
		if err != nil {
			return nil, err
		}
		customData, cse, err := bootstrapper.GetCustomDataAndCSE(ctx)
		if err != nil {
			return nil, fmt.Errorf("rendering custom scripts bootstrap data: %w", err)
		}
		result.CustomData = customData
		result.CSE = cse
	}

	return result, nil
}

// buildScriptlessBootstrapper constructs a scriptless.AKS directly from provider fields and parameters.
// This replaces the ImageFamily.ScriptlessCustomData() indirection — all 5 image families
// built identical scriptless.AKS structs, so no polymorphism was needed.
func (p *DefaultVMProvider) buildScriptlessBootstrapper(
	opts *options.Options,
	instanceType *corecloudprovider.InstanceType,
	kubeletConfig *bootstrap.KubeletConfiguration,
	generalTaints, startupTaints []v1.Taint,
	labels map[string]string,
	subnetID, arch, kubernetesVersion string,
) scriptless.AKS {
	allTaints := lo.Flatten([][]v1.Taint{generalTaints, startupTaints})
	return scriptless.AKS{
		Options: bootstrap.Options{
			ClusterName:      opts.ClusterName,
			ClusterEndpoint:  p.clusterEndpoint,
			KubeletConfig:    kubeletConfig,
			Taints:           allTaints,
			Labels:           labels,
			CABundle:         p.caBundle,
			GPUNode:          utils.IsNvidiaEnabledSKU(instanceType.Name),
			GPUDriverVersion: utils.GetGPUDriverVersion(instanceType.Name),
			GPUDriverType:    utils.GetGPUDriverType(instanceType.Name),
			GPUImageSHA:      utils.GetAKSGPUImageSHA(instanceType.Name),
			SubnetID:         subnetID,
		},
		Arch:                           arch,
		TenantID:                       p.tenantID,
		SubscriptionID:                 p.subscriptionID,
		KubeletIdentityClientID:        opts.KubeletIdentityClientID,
		Location:                       p.location,
		ResourceGroup:                  p.resourceGroup,
		ClusterID:                      opts.ClusterID,
		APIServerName:                  opts.GetAPIServerName(),
		KubeletClientTLSBootstrapToken: opts.KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  getAgentbakerNetworkPlugin(opts),
		NetworkPolicy:                  opts.NetworkPolicy,
		KubernetesVersion:              kubernetesVersion,
	}
}

// buildCustomScriptsBootstrapper constructs a ProvisionClientBootstrap directly.
// This replaces the ImageFamily.CustomScriptsNodeBootstrapping() indirection — the only
// per-family difference was the OSSKU constant, which we derive from the image family name.
func (p *DefaultVMProvider) buildCustomScriptsBootstrapper(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	instanceType *corecloudprovider.InstanceType,
	imageID string,
	kubeletConfig *bootstrap.KubeletConfiguration,
	generalTaints, startupTaints []v1.Taint,
	labels map[string]string,
	subnetID, arch, kubernetesVersion string,
) (customscripts.Bootstrapper, error) {
	opts := options.FromContext(ctx)

	// Resolve imageDistro — needed by the RP's node bootstrapping API
	imageFamily := imagefamily.GetImageFamily(nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, kubernetesVersion)
	useSIG := opts.UseSIG
	imageDistro, err := imagefamily.MapToImageDistro(imageID, nodeClass.Spec.FIPSMode, imageFamily, useSIG)
	if err != nil {
		return nil, fmt.Errorf("mapping image to distro: %w", err)
	}

	// Resolve storage profile (ephemeral vs managed) — needed by bootstrapping API
	sku, err := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
	if err != nil {
		return nil, fmt.Errorf("getting instance type %q for storage profile: %w", instanceType.Name, err)
	}
	storageProfile := consts.StorageProfileManagedDisks
	if instancetype.UseEphemeralDisk(sku, nodeClass) {
		storageProfile = consts.StorageProfileEphemeral
	}

	// Derive OSSKU from image family name — the only per-family difference
	ossku := imageFamilyToOSSKU(nodeClass.Spec.ImageFamily, kubernetesVersion)

	return customscripts.ProvisionClientBootstrap{
		ClusterName:                    opts.ClusterName,
		KubeletConfig:                  kubeletConfig,
		Taints:                         generalTaints,
		StartupTaints:                  startupTaints,
		Labels:                         labels,
		SubnetID:                       subnetID,
		Arch:                           arch,
		SubscriptionID:                 p.subscriptionID,
		ResourceGroup:                  p.resourceGroup,
		ClusterResourceGroup:           p.clusterResourceGroup,
		KubeletClientTLSBootstrapToken: opts.KubeletClientTLSBootstrapToken,
		KubernetesVersion:              kubernetesVersion,
		ImageDistro:                    imageDistro,
		InstanceType:                   instanceType,
		StorageProfile:                 storageProfile,
		NodeBootstrappingProvider:      p.nodeBootstrappingProvider,
		OSSKU:                          ossku,
		FIPSMode:                       nodeClass.Spec.FIPSMode,
		LocalDNSProfile:                nodeClass.Spec.LocalDNS,
		ArtifactStreaming:              nodeClass.Spec.ArtifactStreaming,
	}, nil
}

// imageFamilyToOSSKU maps the image family name to the OSSKU constant used by the
// node bootstrapping API. This was previously the only per-family logic in the
// ImageFamily.CustomScriptsNodeBootstrapping() methods.
func imageFamilyToOSSKU(familyName *string, kubernetesVersion string) string {
	switch lo.FromPtr(familyName) {
	case v1beta1.Ubuntu2204ImageFamily:
		return customscripts.ImageFamilyOSSKUUbuntu2204
	case v1beta1.Ubuntu2404ImageFamily:
		return customscripts.ImageFamilyOSSKUUbuntu2404
	case v1beta1.AzureLinuxImageFamily:
		if imagefamily.UseAzureLinux3(kubernetesVersion) {
			return customscripts.ImageFamilyOSSKUAzureLinux3
		}
		return customscripts.ImageFamilyOSSKUAzureLinux2
	case v1beta1.UbuntuImageFamily:
		fallthrough
	default:
		if lo.FromPtr(familyName) == "" || imagefamily.UseUbuntu2404(kubernetesVersion) {
			return customscripts.ImageFamilyOSSKUUbuntu2404
		}
		return customscripts.ImageFamilyOSSKUUbuntu2204
	}
}

// prepareKubeletConfiguration builds the KubeletConfiguration from nodeClass, instance type, and options.
func prepareKubeletConfiguration(ctx context.Context, instanceType *corecloudprovider.InstanceType, nodeClass *v1beta1.AKSNodeClass) *bootstrap.KubeletConfiguration {
	opts := options.FromContext(ctx)
	kubeletConfig := &bootstrap.KubeletConfiguration{}

	if nodeClass.Spec.Kubelet != nil {
		kubeletConfig.KubeletConfiguration = *nodeClass.Spec.Kubelet
	}

	kubeletConfig.MaxPods = utils.GetMaxPods(nodeClass, opts.NetworkPlugin, opts.NetworkPluginMode)
	kubeletConfig.ClusterDNSServiceIP = opts.DNSServiceIP
	kubeletConfig.KubeReserved = utils.StringMap(instanceType.Overhead.KubeReserved)
	kubeletConfig.SystemReserved = utils.StringMap(instanceType.Overhead.SystemReserved)
	kubeletConfig.EvictionHard = map[string]string{instancetype.MemoryAvailable: instanceType.Overhead.EvictionThreshold.Memory().String()}
	return kubeletConfig
}

// resolveLabels computes the merged labels for the node.
func resolveLabels(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
	instanceType *corecloudprovider.InstanceType,
	capacityType string,
	arch string,
) (map[string]string, error) {
	claimLabels := labelpkg.GetFilteredSingleValuedRequirementLabels(
		scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...),
		func(k string, req *scheduling.Requirement) bool {
			return labelpkg.CanKubeletSetLabel(k)
		},
	)
	additionalLabels := lo.Assign(
		claimLabels,
		labelpkg.GetAllSingleValuedRequirementLabels(instanceType.Requirements),
		map[string]string{karpv1.CapacityTypeLabelKey: capacityType},
	)

	baseLabels, err := labelpkg.Get(ctx, nodeClass, arch)
	if err != nil {
		return nil, err
	}
	return lo.Assign(baseLabels, lo.Assign(nodeClaim.Labels, additionalLabels)), nil
}

// resolveArch determines architecture from instance type requirements.
func resolveArch(instanceType *corecloudprovider.InstanceType) string {
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(
		scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64),
	)); err == nil {
		return karpv1.ArchitectureArm64
	}
	return karpv1.ArchitectureAmd64
}

func getAgentbakerNetworkPlugin(opts *options.Options) string {
	if opts.IsAzureCNIOverlay() || opts.IsCiliumNodeSubnet() || opts.IsNetworkPluginNone() {
		return consts.NetworkPluginNone
	}
	return consts.NetworkPluginAzure
}

// configureStorageProfile builds the StorageProfile for the VM.
func (p *DefaultVMProvider) configureStorageProfile(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
	instanceType *corecloudprovider.InstanceType,
	imageID string,
	vmName string,
) (*armcompute.StorageProfile, error) {
	opts := options.FromContext(ctx)

	osDisk := &armcompute.OSDisk{
		Name:         lo.ToPtr(vmName),
		DiskSizeGB:   nodeClass.Spec.OSDiskSizeGB,
		CreateOption: lo.ToPtr(armcompute.DiskCreateOptionTypesFromImage),
		DeleteOption: lo.ToPtr(armcompute.DiskDeleteOptionTypesDelete),
	}

	// Ephemeral disk
	sku, err := p.instanceTypeProvider.Get(ctx, nodeClass, instanceType.Name)
	if err != nil {
		return nil, fmt.Errorf("getting instance type %q for storage profile: %w", instanceType.Name, err)
	}
	if instancetype.UseEphemeralDisk(sku, nodeClass) {
		_, placement := instancetype.FindMaxEphemeralSizeGBAndPlacement(sku)
		osDisk.DiffDiskSettings = &armcompute.DiffDiskSettings{
			Option:    lo.ToPtr(armcompute.DiffDiskOptionsLocal),
			Placement: placement,
		}
		osDisk.Caching = lo.ToPtr(armcompute.CachingTypesReadOnly)
	}

	// Disk encryption
	if p.diskEncryptionSetID != "" {
		if osDisk.ManagedDisk == nil {
			osDisk.ManagedDisk = &armcompute.ManagedDiskParameters{}
		}
		osDisk.ManagedDisk.DiskEncryptionSet = &armcompute.DiskEncryptionSetParameters{
			ID: lo.ToPtr(p.diskEncryptionSetID),
		}
	}

	// Image reference
	var imageRef *armcompute.ImageReference
	if opts.UseSIG {
		imageRef = &armcompute.ImageReference{
			ID: lo.ToPtr(imageID),
		}
	} else {
		imageRef = &armcompute.ImageReference{
			CommunityGalleryImageID: lo.ToPtr(imageID),
		}
	}

	return &armcompute.StorageProfile{
		OSDisk:         osDisk,
		ImageReference: imageRef,
	}, nil
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
func configureOSProfile(opts *options.Options, vmName string, customData string) *armcompute.OSProfile {
	return &armcompute.OSProfile{
		AdminUsername: lo.ToPtr(opts.LinuxAdminUsername),
		ComputerName:  &vmName,
		CustomData:    lo.ToPtr(customData),
		LinuxConfiguration: &armcompute.LinuxConfiguration{
			DisablePasswordAuthentication: lo.ToPtr(true),
			SSH: &armcompute.SSHConfiguration{
				PublicKeys: []*armcompute.SSHPublicKey{
					{
						KeyData: lo.ToPtr(opts.SSHPublicKey),
						Path:    lo.ToPtr("/home/" + opts.LinuxAdminUsername + "/.ssh/authorized_keys"),
					},
				},
			},
		},
	}
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
