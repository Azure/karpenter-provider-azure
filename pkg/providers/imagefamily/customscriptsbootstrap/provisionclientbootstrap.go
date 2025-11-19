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

package customscriptsbootstrap

import (
	"context"
	"fmt"
	"math"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

const (
	ImageFamilyOSSKUUbuntu2004  = "Ubuntu2004"
	ImageFamilyOSSKUUbuntu2204  = "Ubuntu2204"
	ImageFamilyOSSKUUbuntu2404  = "Ubuntu2404"
	ImageFamilyOSSKUAzureLinux2 = "AzureLinux2"
	ImageFamilyOSSKUAzureLinux3 = "AzureLinux3"
)

type ProvisionClientBootstrap struct {
	ClusterName                    string
	KubeletConfig                  *bootstrap.KubeletConfiguration
	Taints                         []v1.Taint        `hash:"set"`
	StartupTaints                  []v1.Taint        `hash:"set"`
	Labels                         map[string]string `hash:"set"`
	SubnetID                       string
	Arch                           string
	SubscriptionID                 string
	ClusterResourceGroup           string
	ResourceGroup                  string
	KubeletClientTLSBootstrapToken string
	KubernetesVersion              string
	ImageDistro                    string
	IsWindows                      bool
	InstanceType                   *cloudprovider.InstanceType
	StorageProfile                 string
	OSSKU                          string
	NodeBootstrappingProvider      types.NodeBootstrappingAPI
	FIPSMode                       *v1beta1.FIPSMode
	LocalDNSProfile                *v1beta1.LocalDNS
}

var _ Bootstrapper = (*ProvisionClientBootstrap)(nil) // assert ProvisionClientBootstrap implements customscriptsbootstrapper

// nolint gocyclo - will be refactored later
func (p ProvisionClientBootstrap) GetCustomDataAndCSE(ctx context.Context) (string, string, error) {
	provisionValues, err := p.ConstructProvisionValues(ctx)
	if err != nil {
		return "", "", fmt.Errorf("constructProvisionValues failed with error: %w", err)
	}

	if p.NodeBootstrappingProvider == nil {
		return "", "", fmt.Errorf("nodeBootstrapping provider is not initialized")
	}
	nodeBootstrapping, err := p.NodeBootstrappingProvider.Get(ctx, provisionValues)
	if err != nil {
		// As of now we just fail the provisioning given the unlikely scenario of retriable error, but could be revisited along with retriable status on the server side.
		return "", "", fmt.Errorf("nodeBootstrapping.Get failed with error: %w", err)
	}

	customDataHydrated, cseHydrated, err := hydrateBootstrapTokenIfNeeded(nodeBootstrapping.CustomDataEncodedDehydratable, nodeBootstrapping.CSEDehydratable, p.KubeletClientTLSBootstrapToken)
	if err != nil {
		return "", "", fmt.Errorf("hydrateBootstrapTokenIfNeeded failed with error: %w", err)
	}

	return customDataHydrated, cseHydrated, nil
}

// nolint: gocyclo
func (p *ProvisionClientBootstrap) ConstructProvisionValues(ctx context.Context) (*models.ProvisionValues, error) {
	if p.IsWindows {
		// TODO(Windows)
		return nil, fmt.Errorf("windows is not supported")
	}

	nodeLabels := lo.Assign(map[string]string{}, p.Labels)

	// artifact streaming is not yet supported for Arm64, for Ubuntu 20.04, Ubuntu 24.04, and for Azure Linux v3
	// enableArtifactStreaming := p.Arch == karpv1.ArchitectureAmd64 &&
	//		(p.OSSKU == ImageFamilyOSSKUUbuntu2204 || p.OSSKU == ImageFamilyOSSKUAzureLinux2)
	// Temporarily disable artifact streaming altogether, until node provisioning performance is fixed
	// (or until we make artifact streaming configurable)
	enableArtifactStreaming := false

	// unspecified FIPSMode is effectively no FIPS for now
	enableFIPS := lo.FromPtr(p.FIPSMode) == v1beta1.FIPSModeFIPS

	provisionProfile := &models.ProvisionProfile{
		Name:                     lo.ToPtr(""),
		Architecture:             lo.ToPtr(lo.Ternary(p.Arch == karpv1.ArchitectureAmd64, "x64", "Arm64")),
		OsType:                   lo.ToPtr(lo.Ternary(p.IsWindows, models.OSTypeWindows, models.OSTypeLinux)),
		VMSize:                   lo.ToPtr(p.InstanceType.Name),
		Distro:                   lo.ToPtr(p.ImageDistro),
		CustomNodeLabels:         nodeLabels,
		OrchestratorVersion:      lo.ToPtr(p.KubernetesVersion),
		VnetSubnetID:             lo.ToPtr(p.SubnetID),
		StorageProfile:           lo.ToPtr(p.StorageProfile),
		NodeInitializationTaints: lo.Map(p.StartupTaints, func(taint v1.Taint, _ int) string { return taint.ToString() }),
		NodeTaints:               lo.Map(p.Taints, func(taint v1.Taint, _ int) string { return taint.ToString() }),
		SecurityProfile: &models.AgentPoolSecurityProfile{
			SSHAccess: lo.ToPtr(models.SSHAccessLocalUser),
			// EnableVTPM:       lo.ToPtr(false), // Unsupported as of now (Trusted launch)
			// EnableSecureBoot: lo.ToPtr(false), // Unsupported as of now (Trusted launch)
		},
		MaxPods: lo.ToPtr(p.KubeletConfig.MaxPods),

		VnetCidrs: []string{}, // Unsupported as of now; TODO(Windows)
		// MessageOfTheDay:         lo.ToPtr(""),                                    // Unsupported as of now
		// AgentPoolWindowsProfile: &models.AgentPoolWindowsProfile{},               // Unsupported as of now; TODO(Windows)
		// KubeletDiskType:         lo.ToPtr(models.KubeletDiskTypeUnspecified),    // Unsupported as of now
		// CustomLinuxOSConfig:     &models.CustomLinuxOSConfig{},                   // Unsupported as of now (sysctl)
		EnableFIPS: lo.ToPtr(enableFIPS),
		// GpuInstanceProfile:      lo.ToPtr(models.GPUInstanceProfileUnspecified), // Unsupported as of now (MIG)
		// WorkloadRuntime:         lo.ToPtr(models.WorkloadRuntimeUnspecified),    // Unsupported as of now (Kata)
		ArtifactStreamingProfile: &models.ArtifactStreamingProfile{
			Enabled: lo.ToPtr(enableArtifactStreaming),
		},
		LocalDNSProfile: convertLocalDNSToModel(p.LocalDNSProfile),
	}

	// Map OS SKU to AKS provision client's expectation
	// Note that the direction forward is to be more specific with OS versions. Be careful when supporting new ones.
	switch p.OSSKU {
	// https://go.dev/wiki/Switch#multiple-cases
	case ImageFamilyOSSKUUbuntu2004, ImageFamilyOSSKUUbuntu2204, ImageFamilyOSSKUUbuntu2404:
		provisionProfile.OsSku = to.Ptr(models.OSSKUUbuntu)
	case ImageFamilyOSSKUAzureLinux2, ImageFamilyOSSKUAzureLinux3:
		provisionProfile.OsSku = to.Ptr(models.OSSKUAzureLinux)
	default:
		return nil, fmt.Errorf("unsupported OSSKU %s", p.OSSKU)
	}

	if p.KubeletConfig != nil {
		provisionProfile.CustomKubeletConfig = &models.CustomKubeletConfig{
			CPUCfsQuota:           p.KubeletConfig.CPUCFSQuota,
			ImageGcHighThreshold:  p.KubeletConfig.ImageGCHighThresholdPercent,
			ImageGcLowThreshold:   p.KubeletConfig.ImageGCLowThresholdPercent,
			ContainerLogMaxSizeMB: convertContainerLogMaxSizeToMB(p.KubeletConfig.ContainerLogMaxSize),
			ContainerLogMaxFiles:  p.KubeletConfig.ContainerLogMaxFiles,
			PodMaxPids:            convertPodMaxPids(p.KubeletConfig.PodPidsLimit),
		}

		// NodeClaim defaults don't work somehow and keep giving invalid values. Can be improved later.
		if p.KubeletConfig.CPUCFSQuotaPeriod.Duration.String() != "0s" {
			provisionProfile.CustomKubeletConfig.CPUCfsQuotaPeriod = lo.ToPtr(p.KubeletConfig.CPUCFSQuotaPeriod.Duration.String())
		}
		if p.KubeletConfig.CPUManagerPolicy != "" {
			provisionProfile.CustomKubeletConfig.CPUManagerPolicy = lo.ToPtr(p.KubeletConfig.CPUManagerPolicy)
		}
		if p.KubeletConfig.TopologyManagerPolicy != "" {
			provisionProfile.CustomKubeletConfig.TopologyManagerPolicy = lo.ToPtr(p.KubeletConfig.TopologyManagerPolicy)
		}
		if len(p.KubeletConfig.AllowedUnsafeSysctls) > 0 {
			provisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls = p.KubeletConfig.AllowedUnsafeSysctls
		}
	}

	if modeString, ok := p.Labels[v1beta1.AKSLabelMode]; ok && modeString == v1beta1.ModeSystem {
		provisionProfile.Mode = lo.ToPtr(models.AgentPoolModeSystem)
	} else {
		provisionProfile.Mode = lo.ToPtr(models.AgentPoolModeUser)
	}

	if utils.IsNvidiaEnabledSKU(p.InstanceType.Name) {
		provisionProfile.GpuProfile = &models.GPUProfile{
			DriverType:       lo.ToPtr(lo.Ternary(utils.UseGridDrivers(p.InstanceType.Name), models.DriverTypeGRID, models.DriverTypeCUDA)),
			InstallGPUDriver: lo.ToPtr(true),
		}
	}

	provisionHelperValues := &models.ProvisionHelperValues{
		SkuCPU:    lo.ToPtr(p.InstanceType.Capacity.Cpu().AsApproximateFloat64()),
		SkuMemory: lo.ToPtr(math.Ceil(reverseVMMemoryOverhead(options.FromContext(ctx).VMMemoryOverheadPercent, p.InstanceType.Capacity.Memory().AsApproximateFloat64()) / 1024 / 1024 / 1024)),
	}

	return &models.ProvisionValues{
		ProvisionProfile:      provisionProfile,
		ProvisionHelperValues: provisionHelperValues,
	}, nil
}
