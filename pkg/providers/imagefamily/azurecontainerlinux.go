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
	"fmt"

	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/samber/lo"
)

const (
	AzureContainerLinuxGen2ImageDefinition        = "aclgen2TL"
	AzureContainerLinuxGen2ArmImageDefinition     = "aclgen2arm64TL"
	AzureContainerLinuxGen2FIPSImageDefinition    = "aclgen2fipsTL"
	AzureContainerLinuxGen2ArmFIPSImageDefinition = "aclgen2arm64fipsTL"
)

type AzureContainerLinux struct {
	Options *parameters.StaticParameters
}

func (a AzureContainerLinux) Name() string {
	return v1beta1.AzureContainerLinuxImageFamily
}

func (a AzureContainerLinux) DefaultImages(_ bool, fipsMode *v1beta1.FIPSMode, _ bool) []types.DefaultImageOutput {
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
		return []types.DefaultImageOutput{
			azureContainerLinuxImage(AzureContainerLinuxGen2FIPSImageDefinition, "aks-acl-gen2-fips-tl", karpv1.ArchitectureAmd64),
			azureContainerLinuxImage(AzureContainerLinuxGen2ArmFIPSImageDefinition, "aks-acl-arm64-gen2-fips-tl", karpv1.ArchitectureArm64),
		}
	}
	return []types.DefaultImageOutput{
		azureContainerLinuxImage(AzureContainerLinuxGen2ImageDefinition, "aks-acl-gen2-tl", karpv1.ArchitectureAmd64),
		azureContainerLinuxImage(AzureContainerLinuxGen2ArmImageDefinition, "aks-acl-arm64-gen2-tl", karpv1.ArchitectureArm64),
	}
}

func azureContainerLinuxImage(imageDefinition, distro, architecture string) types.DefaultImageOutput {
	return types.DefaultImageOutput{
		PublicGalleryURL:     AKSAzureLinuxPublicGalleryURL,
		GalleryResourceGroup: AKSAzureLinuxResourceGroup,
		GalleryName:          AKSAzureLinuxGalleryName,
		ImageDefinition:      imageDefinition,
		Requirements: scheduling.NewRequirements(
			scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, architecture),
			scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, v1beta1.HyperVGenerationV2),
		),
		Distro: distro,
	}
}

type unsupportedScriptlessBootstrap struct{}

func (unsupportedScriptlessBootstrap) Script() (string, error) {
	return "", fmt.Errorf("AzureContainerLinux requires bootstrappingclient or an AKS Machine API provision mode")
}

func (a AzureContainerLinux) ScriptlessCustomData(
	_ *bootstrap.KubeletConfiguration,
	_ []v1.Taint,
	_ map[string]string,
	_ *string,
	_ *cloudprovider.InstanceType,
) bootstrap.Bootstrapper {
	return unsupportedScriptlessBootstrap{}
}

func (a AzureContainerLinux) CustomScriptsNodeBootstrapping(
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
	vtpmEnabled *bool,
	secureBootEnabled *bool,
) customscriptsbootstrap.Bootstrapper {
	return customscriptsbootstrap.ProvisionClientBootstrap{
		ClusterName:                    a.Options.ClusterName,
		KubeletConfig:                  kubeletConfig,
		Taints:                         taints,
		StartupTaints:                  startupTaints,
		Labels:                         labels,
		SubnetID:                       a.Options.SubnetID,
		Arch:                           a.Options.Arch,
		SubscriptionID:                 a.Options.SubscriptionID,
		ResourceGroup:                  a.Options.ResourceGroup,
		KubeletClientTLSBootstrapToken: a.Options.KubeletClientTLSBootstrapToken,
		KubernetesVersion:              a.Options.KubernetesVersion,
		ImageDistro:                    imageDistro,
		InstanceType:                   instanceType,
		StorageProfile:                 storageProfile,
		ClusterResourceGroup:           a.Options.ClusterResourceGroup,
		GPUDriverInstallationEnabled:   a.Options.GPUDriverInstallationEnabled,
		NodeBootstrappingProvider:      nodeBootstrappingClient,
		OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUAzureContainerLinux,
		FIPSMode:                       fipsMode,
		LocalDNSProfile:                localDNS,
		ArtifactStreaming:              artifactStreaming,
		LinuxOSConfig:                  linuxOSConfig,
		VTPMEnabled:                    vtpmEnabled,
		SecureBootEnabled:              secureBootEnabled,
	}
}
