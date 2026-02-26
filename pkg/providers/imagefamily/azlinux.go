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
	AzureLinuxGen2ImageDefinition      = "V2gen2"
	AzureLinuxGen1ImageDefinition      = "V2"
	AzureLinuxGen2ArmImageDefinition   = "V2gen2arm64"
	AzureLinux2Gen2FIPSImageDefinition = "V2gen2fips"
	AzureLinux2Gen1FIPSImageDefinition = "V2fips"
)

type AzureLinux struct {
	Options *parameters.StaticParameters
}

func (u AzureLinux) Name() string {
	return "AzureLinux2"
}

func (u AzureLinux) DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode) []types.DefaultImageOutput {
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
				ImageDefinition:      AzureLinux2Gen2FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
				),
				Distro: "aks-azurelinux-v2-gen2-fips",
			},
			{
				PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
				GalleryResourceGroup: AKSAzureLinuxResourceGroup,
				GalleryName:          AKSAzureLinuxGalleryName,
				ImageDefinition:      AzureLinux2Gen1FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV1),
				),
				Distro: "aks-azurelinux-v2-fips",
			},
		}
	}
	// image provider will select these images in order, first match wins. This is why we chose to put Gen2 first in the defaultImages, as we prefer gen2 over gen1
	return []types.DefaultImageOutput{
		{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinuxGen2ImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-azurelinux-v2-gen2",
		},
		{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinuxGen1ImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV1),
			),
			Distro: "aks-azurelinux-v2",
		},
		{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinuxGen2ArmImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-azurelinux-v2-arm64-gen2",
		},
	}
}

// UserData returns the default userdata script for the image Family
func (u AzureLinux) ScriptlessCustomData(
	kubeletConfig *bootstrap.KubeletConfiguration,
	taints []v1.Taint,
	labels map[string]string,
	caBundle *string,
	_ *cloudprovider.InstanceType,
) bootstrap.Bootstrapper {
	return bootstrap.AKS{
		Options: bootstrap.Options{
			ClusterName:      u.Options.ClusterName,
			ClusterEndpoint:  u.Options.ClusterEndpoint,
			KubeletConfig:    kubeletConfig,
			Taints:           taints,
			Labels:           labels,
			CABundle:         caBundle,
			GPUNode:          u.Options.GPUNode,
			GPUDriverVersion: u.Options.GPUDriverVersion,
			GPUDriverType:    u.Options.GPUDriverType,
			GPUImageSHA:      u.Options.GPUImageSHA,
			SubnetID:         u.Options.SubnetID,
		},
		Arch:                           u.Options.Arch,
		TenantID:                       u.Options.TenantID,
		SubscriptionID:                 u.Options.SubscriptionID,
		Location:                       u.Options.Location,
		KubeletIdentityClientID:        u.Options.KubeletIdentityClientID,
		ResourceGroup:                  u.Options.ResourceGroup,
		ClusterID:                      u.Options.ClusterID,
		APIServerName:                  u.Options.APIServerName,
		KubeletClientTLSBootstrapToken: u.Options.KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  u.Options.NetworkPlugin,
		NetworkPolicy:                  u.Options.NetworkPolicy,
		KubernetesVersion:              u.Options.KubernetesVersion,
	}
}

// UserData returns the default userdata script for the image Family
func (u AzureLinux) CustomScriptsNodeBootstrapping(
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
	artifactStreaming *v1beta1.ArtifactStreamingMode,
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
		NodeBootstrappingProvider:      nodeBootstrappingClient,
		OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
		FIPSMode:                       fipsMode,
		LocalDNSProfile:                localDNS,
		ArtifactStreaming:              artifactStreaming,
	}
}
