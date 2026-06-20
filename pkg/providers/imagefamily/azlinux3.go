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

package imagefamily

import (
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/samber/lo"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	AzureLinux3Gen2ImageDefinition          = "V3gen2"
	AzureLinux3Gen1ImageDefinition          = "V3"
	AzureLinux3Gen2ArmImageDefinition       = "V3gen2arm64"
	AzureLinux3Gen2FIPSImageDefinition      = "V3gen2fips"
	AzureLinux3Gen1FIPSImageDefinition      = "V3fips"
	AzureLinux3Gen2Arm64FIPSImageDefinition = "V3gen2arm64fips"
	// AzureLinux3Gen2KataImageDefinition is the AKS Pod Sandboxing (Kata) image variant.
	// The Kata host stack (kata-containers RPM -> /usr/local/bin/containerd-shim-kata-v2)
	// ships only in this image; the standard V3gen2 image carries the containerd Kata
	// runtime config but not the shim binary, so a Kata node must boot this variant.
	// Pod Sandboxing is amd64 + gen2 (nested-virtualization) only.
	AzureLinux3Gen2KataImageDefinition = "V3katagen2"
)

type AzureLinux3 struct {
	Options *parameters.StaticParameters
}

func (u AzureLinux3) Name() string {
	return "AzureLinux3"
}

func (u AzureLinux3) DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode, kataEnabled bool) []types.DefaultImageOutput {
	if kataEnabled {
		// AKS Pod Sandboxing requires the dedicated Kata image variant (see
		// AzureLinux3Gen2KataImageDefinition). Pod Sandboxing is amd64 + gen2 only,
		// so this is the only candidate image. There is no FIPS Kata image, so a
		// FIPS+Kata request is unsatisfiable: return no images (surfaced as
		// ImagesNotFound) rather than silently dropping the FIPS guarantee.
		if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
			return []types.DefaultImageOutput{}
		}
		return []types.DefaultImageOutput{
			{
				PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
				GalleryResourceGroup: AKSAzureLinuxResourceGroup,
				GalleryName:          AKSAzureLinuxGalleryName,
				ImageDefinition:      AzureLinux3Gen2KataImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
				),
				Distro: "aks-azurelinux-v3-kata-gen2",
			},
		}
	}
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
		// Note: FIPS images aren't supported in public galleries, only shared image galleries
		// image provider will select these images in order, first match wins
		if !useSIG {
			return []types.DefaultImageOutput{}
		}
		return []types.DefaultImageOutput{
			{
				PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
				GalleryResourceGroup: AKSAzureLinuxResourceGroup,
				GalleryName:          AKSAzureLinuxGalleryName,
				ImageDefinition:      AzureLinux3Gen2FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
				),
				Distro: "aks-azurelinux-v3-gen2-fips",
			},
			{
				PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
				GalleryResourceGroup: AKSAzureLinuxResourceGroup,
				GalleryName:          AKSAzureLinuxGalleryName,
				ImageDefinition:      AzureLinux3Gen1FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV1),
				),
				Distro: "aks-azurelinux-v3-fips",
			},
			{
				PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
				GalleryResourceGroup: AKSAzureLinuxResourceGroup,
				GalleryName:          AKSAzureLinuxGalleryName,
				ImageDefinition:      AzureLinux3Gen2Arm64FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
				),
				Distro: "aks-azurelinux-v3-arm64-gen2-fips",
			},
		}
	}
	// image provider will select these images in order, first match wins
	return []types.DefaultImageOutput{
		{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinux3Gen2ImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-azurelinux-v3-gen2",
		},
		{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinux3Gen1ImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV1),
			),
			Distro: "aks-azurelinux-v3",
		},
		{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinux3Gen2ArmImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-azurelinux-v3-arm64-gen2",
		},
	}
}

// UserData returns the default userdata script for the image Family
func (u AzureLinux3) ScriptlessCustomData(
	kubeletConfig *bootstrap.KubeletConfiguration,
	taints []v1.Taint,
	labels map[string]string,
	caBundle *string,
	_ *cloudprovider.InstanceType,
) bootstrap.Bootstrapper {
	return bootstrap.AKS{
		Options: bootstrap.Options{
			ClusterName:                  u.Options.ClusterName,
			ClusterEndpoint:              u.Options.ClusterEndpoint,
			KubeletConfig:                kubeletConfig,
			Taints:                       taints,
			Labels:                       labels,
			CABundle:                     caBundle,
			GPUNode:                      u.Options.GPUNode,
			GPUDriverVersion:             u.Options.GPUDriverVersion,
			GPUDriverType:                u.Options.GPUDriverType,
			GPUImageSHA:                  u.Options.GPUImageSHA,
			GPUDriverInstallationEnabled: u.Options.GPUDriverInstallationEnabled,
			SubnetID:                     u.Options.SubnetID,
		},
		Arch:                           u.Options.Arch,
		TenantID:                       u.Options.TenantID,
		SubscriptionID:                 u.Options.SubscriptionID,
		Location:                       u.Options.Location,
		KubeletIdentityClientID:        u.Options.KubeletIdentityClientID,
		ResourceGroup:                  u.Options.ResourceGroup,
		NetworkSecurityGroupName:       u.Options.NetworkSecurityGroupName,
		RouteTableName:                 u.Options.RouteTableName,
		APIServerName:                  u.Options.APIServerName,
		KubeletClientTLSBootstrapToken: u.Options.KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  u.Options.NetworkPlugin,
		NetworkPolicy:                  u.Options.NetworkPolicy,
		KubernetesVersion:              u.Options.KubernetesVersion,
	}
}

// UserData returns the default userdata script for the image Family
func (u AzureLinux3) CustomScriptsNodeBootstrapping(
	kubeletConfig *bootstrap.KubeletConfiguration,
	taints []v1.Taint,
	startupTaints []v1.Taint,
	labels map[string]string,
	instanceType *cloudprovider.InstanceType,
	imageDistro string,
	storageProfile string,
	nodeBootstrappingClient types.NodeBootstrappingAPI,
	fipsMode *v1beta1.FIPSMode,
	localDNS *v1beta1.LocalDNS,
	artifactStreaming *v1beta1.ArtifactStreaming,
	linuxOSConfig *v1beta1.LinuxOSConfiguration,
) customscriptsbootstrap.Bootstrapper {
	return customscriptsbootstrap.ProvisionClientBootstrap{
		ClusterName:                    u.Options.ClusterName,
		KubeletConfig:                  kubeletConfig,
		Taints:                         taints,
		StartupTaints:                  startupTaints,
		Labels:                         labels,
		SubnetID:                       u.Options.SubnetID,
		Arch:                           u.Options.Arch,
		SubscriptionID:                 u.Options.SubscriptionID,
		ResourceGroup:                  u.Options.ResourceGroup,
		KubeletClientTLSBootstrapToken: u.Options.KubeletClientTLSBootstrapToken,
		KubernetesVersion:              u.Options.KubernetesVersion,
		ImageDistro:                    imageDistro,
		InstanceType:                   instanceType,
		StorageProfile:                 storageProfile,
		ClusterResourceGroup:           u.Options.ClusterResourceGroup,
		GPUDriverInstallationEnabled:   u.Options.GPUDriverInstallationEnabled,
		NodeBootstrappingProvider:      nodeBootstrappingClient,
		OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
		FIPSMode:                       fipsMode,
		LocalDNSProfile:                localDNS,
		ArtifactStreaming:              artifactStreaming,
		LinuxOSConfig:                  linuxOSConfig,
	}
}
