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
	Ubuntu2204Gen2ImageDefinition    = "2204gen2containerd"
	Ubuntu2204Gen1ImageDefinition    = "2204containerd"
	Ubuntu2204Gen2ArmImageDefinition = "2204gen2arm64containerd"
)

type Ubuntu2204 struct {
	Options *parameters.StaticParameters
}

func (u Ubuntu2204) Name() string {
	return v1beta1.Ubuntu2204ImageFamily
}

func (u Ubuntu2204) DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode) []types.DefaultImageOutput {
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
		// Note: FIPS images aren't supported in public galleries, only shared image galleries
		if !useSIG {
			return []types.DefaultImageOutput{}
		}
		//TODO: Fill out when Ubuntu 22.04 with FIPS becomes available
		return []types.DefaultImageOutput{}
	}
	// image provider will select these images in order, first match wins. This is why we chose to put Ubuntu2204Gen2containerd first in the defaultImages
	return []types.DefaultImageOutput{
		{
			PublicGalleryURL:     AKSUbuntuPublicGalleryURL,
			GalleryResourceGroup: AKSUbuntuResourceGroup,
			GalleryName:          AKSUbuntuGalleryName,
			ImageDefinition:      Ubuntu2204Gen2ImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-ubuntu-containerd-22.04-gen2",
		},
		{
			PublicGalleryURL:     AKSUbuntuPublicGalleryURL,
			GalleryResourceGroup: AKSUbuntuResourceGroup,
			GalleryName:          AKSUbuntuGalleryName,
			ImageDefinition:      Ubuntu2204Gen1ImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV1),
			),
			Distro: "aks-ubuntu-containerd-22.04",
		},
		{
			PublicGalleryURL:     AKSUbuntuPublicGalleryURL,
			GalleryResourceGroup: AKSUbuntuResourceGroup,
			GalleryName:          AKSUbuntuGalleryName,
			ImageDefinition:      Ubuntu2204Gen2ArmImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-ubuntu-arm64-containerd-22.04-gen2",
		},
	}
}

// UserData returns the default userdata script for the image Family
func (u Ubuntu2204) ScriptlessCustomData(
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
func (u Ubuntu2204) CustomScriptsNodeBootstrapping(
	kubeletConfig *bootstrap.KubeletConfiguration,
	taints []v1.Taint,
	startupTaints []v1.Taint,
	labels map[string]string,
	instanceType *cloudprovider.InstanceType,
	imageDistro string,
	storageProfile string,
	nodeBootstrappingClient types.NodeBootstrappingAPI,
	fipsMode *v1beta1.FIPSMode,
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
		OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
		FIPSMode:                       fipsMode,
	}
}
