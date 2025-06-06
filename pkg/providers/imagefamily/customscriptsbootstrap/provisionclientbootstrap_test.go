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

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
				SubnetID:                       "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "sub-id",
				ClusterResourceGroup:           "cluster-rg",
				ResourceGroup:                  "rg",
				KubeletClientTLSBootstrapToken: "abc.123456",
				KubernetesVersion:              "1.26.0",
				ImageDistro:                    "AKSUbuntu",
				IsWindows:                      false,
				StorageProfile:                 "ManagedDisks",
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
				SubnetID:                       "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "sub-id",
				ClusterResourceGroup:           "cluster-rg",
				ResourceGroup:                  "rg",
				KubeletClientTLSBootstrapToken: "abc.123456",
				KubernetesVersion:              "1.26.0",
				ImageDistro:                    "AKSUbuntu",
				IsWindows:                      false,
				StorageProfile:                 "ManagedDisks",
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
				SubnetID:                       "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "sub-id",
				ClusterResourceGroup:           "cluster-rg",
				ResourceGroup:                  "rg",
				KubeletClientTLSBootstrapToken: "abc.123456",
				KubernetesVersion:              "1.26.0",
				ImageDistro:                    "AKSWindows",
				IsWindows:                      true, // This will cause an error
				StorageProfile:                 "ManagedDisks",
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
				SubnetID:                       "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                           karpv1.ArchitectureAmd64,
				SubscriptionID:                 "sub-id",
				ClusterResourceGroup:           "cluster-rg",
				ResourceGroup:                  "rg",
				KubeletClientTLSBootstrapToken: "abc.123456",
				KubernetesVersion:              "1.26.0",
				ImageDistro:                    "AKSUbuntu",
				IsWindows:                      false,
				StorageProfile:                 "ManagedDisks",
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
				KubeletIdentityClientID: "kubelet-client-id",
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

			// For successful cases, verify that the bootstrap token was injected
			if tt.bootstrapper.KubeletClientTLSBootstrapToken != "" {
				assert.Contains(t, cse, tt.bootstrapper.KubeletClientTLSBootstrapToken)

				// Decode customData and check token is present
				decodedCustomData, err := base64.StdEncoding.DecodeString(customData)
				assert.NoError(t, err)
				assert.Contains(t, string(decodedCustomData), tt.bootstrapper.KubeletClientTLSBootstrapToken)
			}
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
				SubnetID:                  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "rg",
				KubernetesVersion:         "1.26.0",
				ImageDistro:               "AKSUbuntu",
				IsWindows:                 false,
				StorageProfile:            "ManagedDisks",
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
				assert.Equal(t, "AKSUbuntu", *profile.Distro)
				assert.Equal(t, "1.26.0", *profile.OrchestratorVersion)
				assert.Equal(t, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", *profile.VnetSubnetID)
				assert.Equal(t, "ManagedDisks", *profile.StorageProfile)
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
				SubnetID:                  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "rg",
				KubernetesVersion:         "1.26.0",
				ImageDistro:               "AKSAzureLinux",
				IsWindows:                 false,
				StorageProfile:            "ManagedDisks",
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
				assert.Equal(t, "AKSAzureLinux", *profile.Distro)

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
				SubnetID:                  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "rg",
				KubernetesVersion:         "1.26.0",
				ImageDistro:               "AKSWindows",
				IsWindows:                 true, // This should cause an error
				StorageProfile:            "ManagedDisks",
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
				SubnetID:                  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "rg",
				KubernetesVersion:         "1.26.0",
				ImageDistro:               "AKSUbuntu",
				IsWindows:                 false,
				StorageProfile:            "ManagedDisks",
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
				SubnetID:                  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureArm64,
				ResourceGroup:             "rg",
				KubernetesVersion:         "1.26.0",
				ImageDistro:               "AKSUbuntu",
				IsWindows:                 false,
				StorageProfile:            "ManagedDisks",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup context with options
			ctx := options.ToContext(context.Background(), &options.Options{
				VMMemoryOverheadPercent: 0.075,
				KubeletIdentityClientID: "kubelet-client-id",
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
