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
	AzureLinuxGen2ImageDefinition      = "V2gen2"
	AzureLinuxGen1ImageDefinition      = "V2"
	AzureLinuxGen2ArmImageDefinition   = "V2gen2arm64"
	AzureLinux2Gen2FIPSImageDefinition = "V2gen2fips"
	AzureLinux2Gen1FIPSImageDefinition = "V2fips"
)

type AzureLinux struct{}

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

