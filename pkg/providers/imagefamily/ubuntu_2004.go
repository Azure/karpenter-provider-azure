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
	Ubuntu2004Gen2FIPSImageDefinition = "2004gen2fipscontainerd"
	Ubuntu2004Gen1FIPSImageDefinition = "2004fipscontainerd"
)

type Ubuntu2004 struct{}

// TODO (charliedmcb): look into .Name() usage, and implications
func (u Ubuntu2004) Name() string {
	return v1beta1.UbuntuImageFamily
}

func (u Ubuntu2004) DefaultImages(useSIG bool, fipsMode *v1beta1.FIPSMode) []types.DefaultImageOutput {
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
		// Note: FIPS images aren't supported in public galleries, only shared image galleries
		// Ubuntu2004 doesn't have default node images (only FIPS)
		if !useSIG {
			return []types.DefaultImageOutput{}
		}
		return []types.DefaultImageOutput{
			{
				PublicGalleryURL:     AKSUbuntuPublicGalleryURL,
				GalleryResourceGroup: AKSUbuntuResourceGroup,
				GalleryName:          AKSUbuntuGalleryName,
				ImageDefinition:      Ubuntu2004Gen2FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
				),
				Distro: "aks-ubuntu-fips-containerd-20.04-gen2",
			},
			{
				PublicGalleryURL:     AKSUbuntuPublicGalleryURL,
				GalleryResourceGroup: AKSUbuntuResourceGroup,
				GalleryName:          AKSUbuntuGalleryName,
				ImageDefinition:      Ubuntu2004Gen1FIPSImageDefinition,
				Requirements: scheduling.NewRequirements(
					scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, karpv1.ArchitectureAmd64),
					scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV1),
				),
				Distro: "aks-ubuntu-fips-containerd-20.04",
			},
		}
	}
	return []types.DefaultImageOutput{}
}

