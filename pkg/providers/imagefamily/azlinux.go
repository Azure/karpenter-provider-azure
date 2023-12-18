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

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter/pkg/providers/launchtemplate/parameters"
	"github.com/Azure/karpenter/pkg/utils"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/scheduling"
)

const (
	AzureLinuxImageFamily           = "AzureLinux"
	AzureLinuxGen2CommunityImage    = "V2gen2"
	AzureLinuxGen1CommunityImage    = "V2"
	AzureLinuxGen2ArmCommunityImage = "V2gen2arm64"
)

type AzureLinux struct {
	Options *parameters.StaticParameters
}

func (u AzureLinux) Name() string {
	return AzureLinuxImageFamily
}

func (u AzureLinux) DefaultImages() []DefaultImageOutput {
	// image provider will select these images in order, first match wins. This is why we chose to put AzureLinuxGen2containerd first in the defaultImages
	return []DefaultImageOutput{
		{
			CommunityImage:   AzureLinuxGen2CommunityImage,
			PublicGalleryURL: AKSAzureLinuxPublicGalleryURL,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureAmd64),
				scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1alpha2.HyperVGenerationV2),
			),
		},
		{
			CommunityImage:   AzureLinuxGen1CommunityImage,
			PublicGalleryURL: AKSAzureLinuxPublicGalleryURL,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureAmd64),
				scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1alpha2.HyperVGenerationV1),
			),
		},
		{
			CommunityImage:   AzureLinuxGen2ArmCommunityImage,
			PublicGalleryURL: AKSUbuntuPublicGalleryURL,
			Requirements: scheduling.NewRequirements(
				scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureArm64),
				scheduling.NewRequirement(v1alpha2.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1alpha2.HyperVGenerationV2),
			),
		},
	}
}

// UserData returns the default userdata script for the image Family
func (u AzureLinux) UserData(kubeletConfig *corev1beta1.KubeletConfiguration, taints []v1.Taint, labels map[string]string, caBundle *string, instanceType *cloudprovider.InstanceType) bootstrap.Bootstrapper {
	var arch string = corev1beta1.ArchitectureAmd64
	if err := instanceType.Requirements.Compatible(scheduling.NewRequirements(scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, corev1beta1.ArchitectureArm64))); err == nil {
		arch = corev1beta1.ArchitectureArm64
	}
	return bootstrap.AKS{
		Options: bootstrap.Options{
			ClusterName:     u.Options.ClusterName,
			ClusterEndpoint: u.Options.ClusterEndpoint,
			KubeletConfig:   kubeletConfig,
			Taints:          taints,
			Labels:          labels,
			CABundle:        caBundle,
			// TODO: Move common calculations that can be shared across image families
			// to shared options struct the user data can reference
			GPUNode:          utils.IsMarinerEnabledGPUSKU(instanceType.Name),
			GPUDriverVersion: utils.GetGPUDriverVersion(instanceType.Name),
		},
		Arch:                           arch,
		TenantID:                       u.Options.TenantID,
		SubscriptionID:                 u.Options.SubscriptionID,
		Location:                       u.Options.Location,
		UserAssignedIdentityID:         u.Options.UserAssignedIdentityID,
		ResourceGroup:                  u.Options.ResourceGroup,
		ClusterID:                      u.Options.ClusterID,
		APIServerName:                  u.Options.APIServerName,
		KubeletClientTLSBootstrapToken: u.Options.KubeletClientTLSBootstrapToken,
		NetworkPlugin:                  u.Options.NetworkPlugin,
		NetworkPolicy:                  u.Options.NetworkPolicy,
		KubernetesVersion:              u.Options.KubernetesVersion,
	}
}
