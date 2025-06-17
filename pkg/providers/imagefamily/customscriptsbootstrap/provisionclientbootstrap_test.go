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

package customscriptsbootstrap_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestGetCustomDataAndCSE(t *testing.T) {
	tests := []struct {
		name         string
		bootstrapper *customscriptsbootstrap.ProvisionClientBootstrap
		expectError  bool
		setupMock    func(pcb *customscriptsbootstrap.ProvisionClientBootstrap)
	}{
		{
			name: "Success with valid parameters",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:                    "test-cluster",
				KubeletConfig:                  &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                       "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "test-sub",
				ClusterResourceGroup:           "test-cluster-rg",
				ResourceGroup:                  "test-rg",
				KubeletClientTLSBootstrapToken: "testbtokenid.testbtokensecret",
				KubernetesVersion:              "1.31.0",
				ImageDistro:                    "aks-ubuntu-containerd-22.04-gen2",
				IsWindows:                      false,
				StorageProfile:                 consts.StorageProfileManagedDisks,
				ImageFamily:                    v1beta1.Ubuntu2204ImageFamily,
				NodeBootstrappingProvider:      &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: false,
		},
		{
			name: "Error with nil NodeBootstrapping provider",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:                    "test-cluster",
				KubeletConfig:                  &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                       "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "test-sub",
				ClusterResourceGroup:           "test-cluster-rg",
				ResourceGroup:                  "test-rg",
				KubeletClientTLSBootstrapToken: "testbtokenid.testbtokensecret",
				KubernetesVersion:              "1.31.0",
				ImageDistro:                    "aks-ubuntu-containerd-22.04-gen2",
				IsWindows:                      false,
				StorageProfile:                 consts.StorageProfileManagedDisks,
				ImageFamily:                    v1beta1.Ubuntu2204ImageFamily,
				NodeBootstrappingProvider:      nil,
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: true,
		},
		{
			name: "Error with Windows OS",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:                    "test-cluster",
				KubeletConfig:                  &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                       "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "test-sub",
				ClusterResourceGroup:           "test-cluster-rg",
				ResourceGroup:                  "test-rg",
				KubeletClientTLSBootstrapToken: "testbtokenid.testbtokensecret",
				KubernetesVersion:              "1.31.0",
				ImageDistro:                    "aks-windows-dummy",
				IsWindows:                      true,
				StorageProfile:                 consts.StorageProfileManagedDisks,
				ImageFamily:                    "Windows",
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: true,
		},
		{
			name: "NodeBootstrapping returns error",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:                    "test-cluster",
				KubeletConfig:                  &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                       "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "test-sub",
				ClusterResourceGroup:           "test-cluster-rg",
				ResourceGroup:                  "test-rg",
				KubeletClientTLSBootstrapToken: "testbtokenid.testbtokensecret",
				KubernetesVersion:              "1.31.0",
				ImageDistro:                    "aks-ubuntu-containerd-22.04-gen2",
				IsWindows:                      false,
				StorageProfile:                 consts.StorageProfileManagedDisks,
				ImageFamily:                    v1beta1.Ubuntu2204ImageFamily,
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{
					SimulateDown: true,
				},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup context with options
			ctx := options.ToContext(context.Background(), &options.Options{
				VMMemoryOverheadPercent: 0.075,
				KubeletIdentityClientID: "test-kubelet-client-id",
			})

			// Apply mocks/setup
			if tt.setupMock != nil {
				tt.setupMock(tt.bootstrapper)
			}

			// Call the function
			customData, cse, err := tt.bootstrapper.GetCustomDataAndCSE(ctx)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotEmpty(t, customData)
			assert.NotEmpty(t, cse)

			// Verify that the bootstrap token template is not present in the output
			// This ensures proper hydration or that no token template was needed
			assert.NotContains(t, cse, "{{.TokenID}}.{{.TokenSecret}}")

			// Check CustomData has no token template
			decodedCustomData, err := base64.StdEncoding.DecodeString(customData)
			assert.NoError(t, err)
			assert.NotContains(t, string(decodedCustomData), "{{.TokenID}}.{{.TokenSecret}}")
		})
	}
}

func TestConstructProvisionValues(t *testing.T) {
	tests := []struct {
		name         string
		bootstrapper *customscriptsbootstrap.ProvisionClientBootstrap
		expectError  bool
		validate     func(t *testing.T, values *models.ProvisionValues)
	}{
		{
			name: "Basic Ubuntu configuration",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-ubuntu-containerd-22.04-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				ImageFamily:               v1beta1.Ubuntu2204ImageFamily,
				Labels:                    map[string]string{"key": "value"},
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, values *models.ProvisionValues) {
				assert.NotNil(t, values.ProvisionProfile)
				assert.NotNil(t, values.ProvisionHelperValues)

				// Check Profile
				profile := values.ProvisionProfile
				assert.Equal(t, "x64", *profile.Architecture)
				assert.Equal(t, models.OSTypeLinux, *profile.OsType)
				assert.Equal(t, models.OSSKUUbuntu, *profile.OsSku)
				assert.Equal(t, "Standard_D2s_v3", *profile.VMSize)
				assert.Equal(t, "aks-ubuntu-containerd-22.04-gen2", *profile.Distro)
				assert.Equal(t, "1.31.0", *profile.OrchestratorVersion)
				assert.Equal(t, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", *profile.VnetSubnetID)
				assert.Equal(t, consts.StorageProfileManagedDisks, *profile.StorageProfile)
				assert.Equal(t, int32(110), *profile.MaxPods)
				assert.Equal(t, models.AgentPoolModeUser, *profile.Mode)

				// Check Helper Values
				helperValues := values.ProvisionHelperValues
				assert.Equal(t, float64(2), *helperValues.SkuCPU)
				assert.InDelta(t, float64(9), *helperValues.SkuMemory, 0.1) // Checking approximate value due to overhead calculation
			},
		},
		{
			name: "Azure Linux configuration",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-azurelinux-v2-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				ImageFamily:               v1beta1.AzureLinuxImageFamily,
				Labels:                    map[string]string{"kubernetes.azure.com/mode": "system"}, // Test system mode
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, values *models.ProvisionValues) {
				assert.NotNil(t, values.ProvisionProfile)

				// Check Profile
				profile := values.ProvisionProfile
				assert.Equal(t, models.OSSKUAzureLinux, *profile.OsSku)
				assert.Equal(t, "aks-azurelinux-v2-gen2", *profile.Distro)

				// Check system mode
				assert.Equal(t, models.AgentPoolModeSystem, *profile.Mode)

				// Check artifact streaming is enabled
				assert.True(t, *profile.ArtifactStreamingProfile.Enabled)
			},
		},
		{
			name: "Windows configuration - should error",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-windows",
				IsWindows:                 true, // This should cause an error
				StorageProfile:            consts.StorageProfileManagedDisks,
				ImageFamily:               "Windows",
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: true,
		},
		{
			name: "GPU instance type",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-ubuntu-containerd-22.04-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				ImageFamily:               v1beta1.Ubuntu2204ImageFamily,
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_NC6s_v3", // GPU instance
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("6"),
						v1.ResourceMemory: resource.MustParse("112Gi"),
						"nvidia.com/gpu":  resource.MustParse("1"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, values *models.ProvisionValues) {
				assert.NotNil(t, values.ProvisionProfile.GpuProfile)
				assert.True(t, *values.ProvisionProfile.GpuProfile.InstallGPUDriver)
				assert.Equal(t, models.DriverTypeCUDA, *values.ProvisionProfile.GpuProfile.DriverType)
			},
		},
		{
			name: "ARM64 architecture",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureArm64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-ubuntu-arm64-containerd-22.04-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				ImageFamily:               v1beta1.Ubuntu2204ImageFamily,
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D2ps_v5", // ARM64 instance
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("2"),
						v1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, values *models.ProvisionValues) {
				assert.Equal(t, "Arm64", *values.ProvisionProfile.Architecture)

				// Artifact streaming should be disabled for ARM64
				assert.False(t, *values.ProvisionProfile.ArtifactStreamingProfile.Enabled)
			},
		},
		{
			name: "With custom kubelet config",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName: "test-cluster",
				KubeletConfig: &bootstrap.KubeletConfiguration{
					MaxPods: int32(110),
					KubeletConfiguration: v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            "static",
						CPUCFSQuota:                 lo.ToPtr(true),
						CPUCFSQuotaPeriod:           metav1.Duration{Duration: 100 * time.Millisecond},
						TopologyManagerPolicy:       "single-numa-node",
						ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
						ImageGCLowThresholdPercent:  lo.ToPtr(int32(75)),
						ContainerLogMaxSize:         "100Mi",
						ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
						PodPidsLimit:                lo.ToPtr(int64(1024)),
						AllowedUnsafeSysctls:        []string{"kernel.msg*", "net.ipv4.route.min_pmtu"},
					},
				},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-ubuntu-containerd-22.04-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				ImageFamily:               v1beta1.Ubuntu2204ImageFamily,
				NodeBootstrappingProvider: &fake.NodeBootstrappingAPI{},
				InstanceType: &cloudprovider.InstanceType{
					Name: "Standard_D8s_v3",
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("8"),
						v1.ResourceMemory: resource.MustParse("32Gi"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, values *models.ProvisionValues) {
				// Validate that CustomKubeletConfig was properly set
				assert.NotNil(t, values.ProvisionProfile.CustomKubeletConfig)

				// CPU config
				assert.True(t, *values.ProvisionProfile.CustomKubeletConfig.CPUCfsQuota)
				assert.Equal(t, "100ms", *values.ProvisionProfile.CustomKubeletConfig.CPUCfsQuotaPeriod)
				assert.Equal(t, "static", *values.ProvisionProfile.CustomKubeletConfig.CPUManagerPolicy)

				// Topology manager
				assert.Equal(t, "single-numa-node", *values.ProvisionProfile.CustomKubeletConfig.TopologyManagerPolicy)

				// Image GC
				assert.Equal(t, int32(85), *values.ProvisionProfile.CustomKubeletConfig.ImageGcHighThreshold)
				assert.Equal(t, int32(75), *values.ProvisionProfile.CustomKubeletConfig.ImageGcLowThreshold)

				// Container logs
				assert.Equal(t, int32(100), *values.ProvisionProfile.CustomKubeletConfig.ContainerLogMaxSizeMB)
				assert.Equal(t, int32(10), *values.ProvisionProfile.CustomKubeletConfig.ContainerLogMaxFiles)

				// Pod PIDs
				assert.Equal(t, int32(1024), *values.ProvisionProfile.CustomKubeletConfig.PodMaxPids)

				// Sysctls
				assert.Len(t, values.ProvisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls, 2)
				assert.Contains(t, values.ProvisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls, "kernel.msg*")
				assert.Contains(t, values.ProvisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls, "net.ipv4.route.min_pmtu")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup context with options
			ctx := options.ToContext(context.Background(), &options.Options{
				VMMemoryOverheadPercent: 0.075,
				KubeletIdentityClientID: "test-kubelet-client-id",
			})

			// Call the function
			values, err := tt.bootstrapper.ConstructProvisionValues(ctx)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, values)

			// Run custom validation if provided
			if tt.validate != nil {
				tt.validate(t, values)
			}
		})
	}
}
