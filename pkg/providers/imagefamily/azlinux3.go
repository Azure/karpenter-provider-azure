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
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/samber/lo"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	AzureLinux3Gen2ImageDefinition          = "V3gen2"
	AzureLinux3Gen1ImageDefinition          = "V3"
	AzureLinux3Gen2ArmImageDefinition       = "V3gen2arm64"
	AzureLinux3Gen2FIPSImageDefinition      = "V3gen2fips"
	AzureLinux3Gen1FIPSImageDefinition      = "V3fips"
	AzureLinux3Gen2Arm64FIPSImageDefinition = "V3gen2arm64fips"
)

type AzureLinux3 struct{}

func (u AzureLinux3) Name() string {
	return "AzureLinux3"
}

func (u AzureLinux3) DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode) []types.DefaultImageOutput {
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
	images := []types.DefaultImageOutput{
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
	}

	if useSIG {
		// AzLinux3 ARM64 VHD is not available in CIG right now
		images = append(images, types.DefaultImageOutput{
			PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
			GalleryResourceGroup: AKSAzureLinuxResourceGroup,
			GalleryName:          AKSAzureLinuxGalleryName,
			ImageDefinition:      AzureLinux3Gen2ArmImageDefinition,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureArm64),
				scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
			),
			Distro: "aks-azurelinux-v3-arm64-gen2",
		})
	}

	return images
}

