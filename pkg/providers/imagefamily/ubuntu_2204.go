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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"

	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

const (
	Ubuntu2204Gen2CommunityImage    = "2204gen2containerd"
	Ubuntu2204Gen1CommunityImage    = "2204containerd"
	Ubuntu2204Gen2ArmCommunityImage = "2204gen2arm64containerd"
)

type Ubuntu2204 struct {
	Options *parameters.StaticParameters
}

func (u Ubuntu2204) Name() string {
	return v1alpha2.Ubuntu2204ImageFamily
}

func (u Ubuntu2204) DefaultImages() []DefaultImageOutput {
	// image provider will select these images in order, first match wins. This is why we chose to put Ubuntu2204Gen2containerd first in the defaultImages
	return []DefaultImageOutput{
		{
			CommunityImage:   Ubuntu2204Gen2CommunityImage,
			PublicGalleryURL: AKSUbuntuPublicGalleryURL,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureAmd64),
				scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1alpha2.HyperVGenerationV2),
			),
		},
		{
			CommunityImage:   Ubuntu2204Gen1CommunityImage,
			PublicGalleryURL: AKSUbuntuPublicGalleryURL,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureAmd64),
				scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1alpha2.HyperVGenerationV1),
			),
		},
		{
			CommunityImage:   Ubuntu2204Gen2ArmCommunityImage,
			PublicGalleryURL: AKSUbuntuPublicGalleryURL,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureArm64),
				scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1alpha2.HyperVGenerationV2),
			),
		},
	}
}

// UserData returns the default userdata script for the image Family
func (u Ubuntu2204) UserData(kubeletConfig *corev1beta1.KubeletConfiguration, taints []v1.Taint, labels map[string]string, caBundle *string, _ *cloudprovider.InstanceType) bootstrap.Bootstrapper {
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
