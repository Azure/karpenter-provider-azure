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
	"encoding/base64"
	"fmt"
	"math"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// NodeBootstrappingProvider defines an interface for retrieving node bootstrapping data
type NodeBootstrappingProvider interface {
	Get(ctx context.Context, parameters *models.ProvisionValues) (string, string, error)
}

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
	ImageFamily                    string
	NodeBootstrapping              NodeBootstrappingProvider
}

var _ Bootstrapper = (*ProvisionClientBootstrap)(nil) // assert ProvisionClientBootstrap implements customscriptsbootstrapper

// nolint gocyclo - will be refactored later
func (p ProvisionClientBootstrap) GetCustomDataAndCSE(ctx context.Context) (string, string, error) {
	if p.IsWindows {
		// TODO(Windows)
		return "", "", fmt.Errorf("windows is not supported")
	}

	nodeLabels := lo.Assign(map[string]string{}, p.Labels)
	// Note that while we set the Kubelet identity label here, the actual kubelet identity that is set in the bootstrapping
	// script is configured by the NPS service. That means the label can be set to the older client ID if the client ID
	// changed recently. This is OK because drift will correct it.
	labels.AddAgentBakerGeneratedLabels(p.ResourceGroup, options.FromContext(ctx).KubeletIdentityClientID, nodeLabels)

	// artifact streaming is not yet supported for Arm64, for Ubuntu 20.04, and for Azure Linux v3
	enableArtifactStreaming := p.Arch == karpv1.ArchitectureAmd64 &&
		(p.ImageFamily == v1beta1.Ubuntu2204ImageFamily || p.ImageFamily == v1beta1.AzureLinuxImageFamily)

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
		// EnableFIPS:              lo.ToPtr(false),                                 // Unsupported as of now
		// GpuInstanceProfile:      lo.ToPtr(models.GPUInstanceProfileUnspecified), // Unsupported as of now (MIG)
		// WorkloadRuntime:         lo.ToPtr(models.WorkloadRuntimeUnspecified),    // Unsupported as of now (Kata)
		ArtifactStreamingProfile: &models.ArtifactStreamingProfile{
			Enabled: lo.ToPtr(enableArtifactStreaming),
		},
	}

	switch p.ImageFamily {
	case v1beta1.Ubuntu2204ImageFamily:
		provisionProfile.OsSku = to.Ptr(models.OSSKUUbuntu)
	case v1beta1.AzureLinuxImageFamily:
		provisionProfile.OsSku = to.Ptr(models.OSSKUAzureLinux)
	default:
		provisionProfile.OsSku = to.Ptr(models.OSSKUUbuntu)
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

	if modeString, ok := p.Labels["kubernetes.azure.com/mode"]; ok && modeString == "system" {
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

	return p.getNodeBootstrappingFromClient(ctx, provisionProfile, provisionHelperValues, p.KubeletClientTLSBootstrapToken)
}

func (p *ProvisionClientBootstrap) getNodeBootstrappingFromClient(ctx context.Context, provisionProfile *models.ProvisionProfile, provisionHelperValues *models.ProvisionHelperValues, bootstrapToken string) (string, string, error) {
	if p.NodeBootstrapping == nil {
		return "", "", fmt.Errorf("nodeBootstrapping provider is not initialized")
	}

	provisionValues := &models.ProvisionValues{
		ProvisionProfile:      provisionProfile,
		ProvisionHelperValues: provisionHelperValues,
	}

	customDataWithoutBootstrapToken, cseWithoutBootstrapToken, err := p.NodeBootstrapping.Get(
		ctx,
		provisionValues,
	)
	if err != nil {
		// As of now we just fail the provisioning given the unlikely scenario of retriable error, but could be revisited along with retriable status on the server side.
		return "", "", err
	}

	cseWithBootstrapToken := strings.ReplaceAll(cseWithoutBootstrapToken, "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken)

	decodedCustomDataWithoutBootstrapTokenInBytes, err := base64.StdEncoding.DecodeString(customDataWithoutBootstrapToken)
	if err != nil {
		return "", "", err
	}
	decodedCustomDataWithBootstrapToken := strings.ReplaceAll(string(decodedCustomDataWithoutBootstrapTokenInBytes), "{{.TokenID}}.{{.TokenSecret}}", bootstrapToken)
	customDataWithBootstrapToken := base64.StdEncoding.EncodeToString([]byte(decodedCustomDataWithBootstrapToken))

	return customDataWithBootstrapToken, cseWithBootstrapToken, nil
}

func reverseVMMemoryOverhead(vmMemoryOverheadPercent float64, adjustedMemory float64) float64 {
	// This is not the best way to do it... But will be refactored later, given that retrieving the original memory properly might involves some restructure.
	// Due to the fact that it is abstracted behind the cloudprovider interface.
	return adjustedMemory / (1 - vmMemoryOverheadPercent)
}

func convertContainerLogMaxSizeToMB(containerLogMaxSize string) *int32 {
	q, err := resource.ParseQuantity(containerLogMaxSize)
	if err == nil {
		// This could be improved later
		return lo.ToPtr(int32(math.Round(q.AsApproximateFloat64() / 1024 / 1024)))
	}
	return nil
}

func convertPodMaxPids(podPidsLimit *int64) *int32 {
	if podPidsLimit != nil {
		podPidsLimitInt64 := *podPidsLimit
		if podPidsLimitInt64 > int64(math.MaxInt32) {
			// This could be improved later
			return lo.ToPtr(int32(math.MaxInt32))
		} else if podPidsLimitInt64 < 0 {
			// This as well
			return lo.ToPtr(int32(-1))
		} else {
			return lo.ToPtr(int32(podPidsLimitInt64)) // golint:ignore G115 already check overflow
		}
	}
	return nil
}
