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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/provisionclients/models"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
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
				OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
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
				OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
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
				OSSKU:                          "Windows",
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
				OSSKU:                          customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
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
			g := NewWithT(t)
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
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(customData).ToNot(BeEmpty())
			g.Expect(cse).ToNot(BeEmpty())

			// Verify that the bootstrap token template is not present in the output
			// This ensures proper hydration or that no token template was needed
			g.Expect(cse).ToNot(ContainSubstring("{{.TokenID}}.{{.TokenSecret}}"))

			// Check CustomData has no token template
			decodedCustomData, err := base64.StdEncoding.DecodeString(customData)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(string(decodedCustomData)).ToNot(ContainSubstring("{{.TokenID}}.{{.TokenSecret}}"))
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
			name: "Basic Ubuntu 2004 configuration",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-ubuntu-fips-containerd-20.04-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUUbuntu2004,
				Labels:                    map[string]string{"key": "value"},
				FIPSMode:                  &v1beta1.FIPSModeFIPS,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile).ToNot(BeNil())
				g.Expect(values.ProvisionHelperValues).ToNot(BeNil())

				// Check Profile
				profile := values.ProvisionProfile
				g.Expect(*profile.Architecture).To(Equal("x64"))
				g.Expect(*profile.OsType).To(Equal(models.OSTypeLinux))
				g.Expect(*profile.OsSku).To(Equal(models.OSSKUUbuntu))
				g.Expect(*profile.VMSize).To(Equal("Standard_D2s_v3"))
				g.Expect(*profile.Distro).To(Equal("aks-ubuntu-fips-containerd-20.04-gen2"))
				g.Expect(*profile.OrchestratorVersion).To(Equal("1.31.0"))
				g.Expect(*profile.VnetSubnetID).To(Equal("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"))
				g.Expect(*profile.StorageProfile).To(Equal(consts.StorageProfileManagedDisks))
				g.Expect(*profile.MaxPods).To(Equal(int32(110)))
				g.Expect(*profile.Mode).To(Equal(models.AgentPoolModeUser))
				g.Expect(*profile.EnableFIPS).To(BeTrue())

				// Check Helper Values
				helperValues := values.ProvisionHelperValues
				g.Expect(*helperValues.SkuCPU).To(Equal(float64(2)))
				g.Expect(*helperValues.SkuMemory).To(BeNumerically("~", float64(9), 0.1)) // Checking approximate value due to overhead calculation
			},
		},
		{
			name: "Basic Ubuntu 2204 configuration",
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
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
				Labels:                    map[string]string{"key": "value"},
				FIPSMode:                  &v1beta1.FIPSModeDisabled,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile).ToNot(BeNil())
				g.Expect(values.ProvisionHelperValues).ToNot(BeNil())

				// Check Profile
				profile := values.ProvisionProfile
				g.Expect(*profile.Architecture).To(Equal("x64"))
				g.Expect(*profile.OsType).To(Equal(models.OSTypeLinux))
				g.Expect(*profile.OsSku).To(Equal(models.OSSKUUbuntu))
				g.Expect(*profile.VMSize).To(Equal("Standard_D2s_v3"))
				g.Expect(*profile.Distro).To(Equal("aks-ubuntu-containerd-22.04-gen2"))
				g.Expect(*profile.OrchestratorVersion).To(Equal("1.31.0"))
				g.Expect(*profile.VnetSubnetID).To(Equal("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"))
				g.Expect(*profile.StorageProfile).To(Equal(consts.StorageProfileManagedDisks))
				g.Expect(*profile.MaxPods).To(Equal(int32(110)))
				g.Expect(*profile.Mode).To(Equal(models.AgentPoolModeUser))
				g.Expect(*profile.EnableFIPS).To(BeFalse())

				// Check Helper Values
				helperValues := values.ProvisionHelperValues
				g.Expect(*helperValues.SkuCPU).To(Equal(float64(2)))
				g.Expect(*helperValues.SkuMemory).To(BeNumerically("~", float64(9), 0.1)) // Checking approximate value due to overhead calculation
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
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile).ToNot(BeNil())

				// Check Profile
				profile := values.ProvisionProfile
				g.Expect(*profile.OsSku).To(Equal(models.OSSKUAzureLinux))
				g.Expect(*profile.Distro).To(Equal("aks-azurelinux-v2-gen2"))

				// Check system mode
				g.Expect(*profile.Mode).To(Equal(models.AgentPoolModeSystem))

				// Check artifact streaming is disabled
				g.Expect(*profile.ArtifactStreamingProfile.Enabled).To(BeFalse())

				// Check FIPS enablement (unset/nil FIPSMode is effectively false for now)
				g.Expect(*profile.EnableFIPS).To(BeFalse())
			},
		},
		{
			name: "Azure Linux 3 configuration",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.32.0",
				ImageDistro:               "aks-azurelinux-v3-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile).ToNot(BeNil())

				// Check Profile
				profile := values.ProvisionProfile
				g.Expect(*profile.OsSku).To(Equal(models.OSSKUAzureLinux))
				g.Expect(*profile.Distro).To(Equal("aks-azurelinux-v3-gen2"))
				g.Expect(*profile.Mode).To(Equal(models.AgentPoolModeUser))

				// Check artifact streaming is disabled for AzureLinux3
				g.Expect(*profile.ArtifactStreamingProfile.Enabled).To(BeFalse())

				// Check FIPS enablement (unset/nil FIPSMode is effectively false for now)
				g.Expect(*profile.EnableFIPS).To(BeFalse())
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
				OSSKU:                     "Windows",
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
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile.GpuProfile).ToNot(BeNil())
				g.Expect(*values.ProvisionProfile.GpuProfile.InstallGPUDriver).To(BeTrue())
				g.Expect(*values.ProvisionProfile.GpuProfile.DriverType).To(Equal(models.DriverTypeCUDA))
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
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
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
				g := NewWithT(t)
				g.Expect(*values.ProvisionProfile.Architecture).To(Equal("Arm64"))

				// Artifact streaming should be disabled for ARM64
				g.Expect(*values.ProvisionProfile.ArtifactStreamingProfile.Enabled).To(BeFalse())
			},
		},
		{
			name: "ARM64 AzureLinux2 configuration",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureArm64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-azurelinux-v2-arm64-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile).ToNot(BeNil())

				// Check Profile
				profile := values.ProvisionProfile
				g.Expect(*profile.Architecture).To(Equal("Arm64"))
				g.Expect(*profile.OsSku).To(Equal(models.OSSKUAzureLinux))
				g.Expect(*profile.Distro).To(Equal("aks-azurelinux-v2-arm64-gen2"))

				// Artifact streaming should be disabled for ARM64
				g.Expect(*profile.ArtifactStreamingProfile.Enabled).To(BeFalse())
			},
		},
		{
			name: "ARM64 AzureLinux3 configuration",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureArm64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.32.0",
				ImageDistro:               "aks-azurelinux-v3-arm64-gen2",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
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
				g := NewWithT(t)
				g.Expect(values.ProvisionProfile).ToNot(BeNil())

				// Check Profile
				profile := values.ProvisionProfile
				g.Expect(*profile.Architecture).To(Equal("Arm64"))
				g.Expect(*profile.OsSku).To(Equal(models.OSSKUAzureLinux))
				g.Expect(*profile.Distro).To(Equal("aks-azurelinux-v3-arm64-gen2"))

				// Artifact streaming should be disabled for ARM64
				g.Expect(*profile.ArtifactStreamingProfile.Enabled).To(BeFalse())
			},
		},
		{
			name: "Unsupported image family - should error",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName:               "test-cluster",
				KubeletConfig:             &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
				SubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
				Arch:                      karpv1.ArchitectureAmd64,
				ResourceGroup:             "test-rg",
				KubernetesVersion:         "1.31.0",
				ImageDistro:               "aks-unknown-distro",
				IsWindows:                 false,
				StorageProfile:            consts.StorageProfileManagedDisks,
				OSSKU:                     "UnsupportedFamily",
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
			name: "With custom kubelet config",
			bootstrapper: &customscriptsbootstrap.ProvisionClientBootstrap{
				ClusterName: "test-cluster",
				KubeletConfig: &bootstrap.KubeletConfiguration{
					MaxPods: int32(110),
					KubeletConfiguration: v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            to.Ptr("static"),
						CPUCFSQuota:                 lo.ToPtr(true),
						CPUCFSQuotaPeriod:           metav1.Duration{Duration: 100 * time.Millisecond},
						TopologyManagerPolicy:       to.Ptr("single-numa-node"),
						ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
						ImageGCLowThresholdPercent:  lo.ToPtr(int32(75)),
						ContainerLogMaxSize:         to.Ptr("100Mi"),
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
				OSSKU:                     customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
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
				g := NewWithT(t)
				// Validate that CustomKubeletConfig was properly set
				g.Expect(values.ProvisionProfile.CustomKubeletConfig).ToNot(BeNil())

				// CPU config
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.CPUCfsQuota).To(BeTrue())
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.CPUCfsQuotaPeriod).To(Equal("100ms"))
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.CPUManagerPolicy).To(Equal("static"))

				// Topology manager
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.TopologyManagerPolicy).To(Equal("single-numa-node"))

				// Image GC
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.ImageGcHighThreshold).To(Equal(int32(85)))
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.ImageGcLowThreshold).To(Equal(int32(75)))

				// Container logs
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.ContainerLogMaxSizeMB).To(Equal(int32(100)))
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.ContainerLogMaxFiles).To(Equal(int32(10)))

				// Pod PIDs
				g.Expect(*values.ProvisionProfile.CustomKubeletConfig.PodMaxPids).To(Equal(int32(1024)))

				// Sysctls
				g.Expect(values.ProvisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls).To(HaveLen(2))
				g.Expect(values.ProvisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls).To(ContainElement("kernel.msg*"))
				g.Expect(values.ProvisionProfile.CustomKubeletConfig.AllowedUnsafeSysctls).To(ContainElement("net.ipv4.route.min_pmtu"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			// Setup context with options
			ctx := options.ToContext(context.Background(), &options.Options{
				VMMemoryOverheadPercent: 0.075,
				KubeletIdentityClientID: "test-kubelet-client-id",
			})

			// Call the function
			values, err := tt.bootstrapper.ConstructProvisionValues(ctx)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(values).ToNot(BeNil())

			// Run custom validation if provided
			if tt.validate != nil {
				tt.validate(t, values)
			}
		})
	}
}

func TestArtifactStreamingEnablement(t *testing.T) {
	baseBootstrapper := &customscriptsbootstrap.ProvisionClientBootstrap{
		ClusterName:                    "test-cluster",
		KubeletConfig:                  &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
		SubnetID:                       "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
		SubscriptionID:                 "test-sub",
		ClusterResourceGroup:           "test-cluster-rg",
		ResourceGroup:                  "test-rg",
		KubeletClientTLSBootstrapToken: "testbtokenid.testbtokensecret",
		IsWindows:                      false,
		StorageProfile:                 consts.StorageProfileManagedDisks,
		NodeBootstrappingProvider:      &fake.NodeBootstrappingAPI{},
		InstanceType: &cloudprovider.InstanceType{
			Name: "Standard_D2s_v3",
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("2"),
				v1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}

	tests := []struct {
		name                             string
		arch                             string
		ossku                            string
		kubernetesVersion                string
		imageDistro                      string
		expectedArtifactStreamingEnabled bool
		description                      string
	}{
		{
			name:                             "AMD64 Ubuntu2004 FIPS - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureAmd64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUUbuntu2004,
			kubernetesVersion:                "1.31.0",
			imageDistro:                      "aks-ubuntu-fips-containerd-20.04-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for AMD64 with Ubuntu2004 FIPS",
		},
		{
			name:                             "AMD64 Ubuntu2204 - Artifact streaming enabled",
			arch:                             karpv1.ArchitectureAmd64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
			kubernetesVersion:                "1.31.0",
			imageDistro:                      "aks-ubuntu-containerd-22.04-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for AMD64 with Ubuntu2204",
		},
		{
			name:                             "AMD64 Ubuntu2404 - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureAmd64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUUbuntu2404,
			kubernetesVersion:                "1.34.0",
			imageDistro:                      "aks-ubuntu-containerd-24.04-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for AMD64 with Ubuntu2404",
		},
		{
			name:                             "AMD64 AzureLinux2 - Artifact streaming enabled",
			arch:                             karpv1.ArchitectureAmd64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
			kubernetesVersion:                "1.31.0",
			imageDistro:                      "aks-azurelinux-v2-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for AMD64 with AzureLinux2",
		},
		{
			name:                             "AMD64 AzureLinux3 - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureAmd64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
			kubernetesVersion:                "1.32.0",
			imageDistro:                      "aks-azurelinux-v3-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for AzureLinux3 even on AMD64",
		},
		{
			name:                             "ARM64 Ubuntu2204 - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureArm64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
			kubernetesVersion:                "1.31.0",
			imageDistro:                      "aks-ubuntu-arm64-containerd-22.04-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for ARM64 architecture",
		},
		{
			name:                             "ARM64 AzureLinux2 - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureArm64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
			kubernetesVersion:                "1.31.0",
			imageDistro:                      "aks-azurelinux-v2-arm64-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for ARM64 architecture even with supported OS",
		},
		{
			name:                             "AMD64 AzureLinux3 - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureAmd64,
			kubernetesVersion:                "1.32.0",
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
			imageDistro:                      "aks-azurelinux-v3-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for AzureLinux3 even on AMD64",
		},
		{
			name:                             "ARM64 AzureLinux3 - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureArm64,
			ossku:                            customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
			kubernetesVersion:                "1.32.0",
			imageDistro:                      "aks-azurelinux-v3-arm64-gen2",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for ARM64 + AzureLinux3 combination",
		},
		{
			name:                             "AMD64 Custom OSSKU - Artifact streaming disabled",
			arch:                             karpv1.ArchitectureAmd64,
			ossku:                            "CustomUnsupportedOSSKU",
			kubernetesVersion:                "1.31.0",
			imageDistro:                      "aks-custom-distro",
			expectedArtifactStreamingEnabled: false,
			description:                      "Artifact streaming should be disabled for unsupported OSSKU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			// Create a copy of the base bootstrapper and modify for this test
			bootstrapper := *baseBootstrapper
			bootstrapper.Arch = tt.arch
			bootstrapper.OSSKU = tt.ossku
			bootstrapper.KubernetesVersion = tt.kubernetesVersion
			bootstrapper.ImageDistro = tt.imageDistro

			// Setup context with options
			ctx := options.ToContext(context.Background(), &options.Options{
				VMMemoryOverheadPercent: 0.075,
				KubeletIdentityClientID: "test-kubelet-client-id",
			})

			values, err := bootstrapper.ConstructProvisionValues(ctx)

			// For unsupported OSSKU, we expect an error and should not continue validation
			if tt.ossku == "CustomUnsupportedOSSKU" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("unsupported OSSKU"))
				return
			}

			// For all other cases, expect success
			g.Expect(err).ToNot(HaveOccurred(), tt.description)
			g.Expect(values).ToNot(BeNil(), "ProvisionValues should not be nil")
			g.Expect(values.ProvisionProfile).ToNot(BeNil(), "ProvisionProfile should not be nil")
			g.Expect(values.ProvisionProfile.ArtifactStreamingProfile).ToNot(BeNil(), "ArtifactStreamingProfile should not be nil")
			g.Expect(values.ProvisionProfile.ArtifactStreamingProfile.Enabled).ToNot(BeNil(), "ArtifactStreamingProfile.Enabled should not be nil")

			// Check artifact streaming enablement
			actualEnabled := *values.ProvisionProfile.ArtifactStreamingProfile.Enabled
			g.Expect(actualEnabled).To(Equal(tt.expectedArtifactStreamingEnabled),
				"Artifact streaming enablement mismatch: %s. Expected: %v, Actual: %v",
				tt.description, tt.expectedArtifactStreamingEnabled, actualEnabled)

			// Additional validation for enabled cases
			if tt.expectedArtifactStreamingEnabled {
				g.Expect(actualEnabled).To(BeTrue(), "Artifact streaming should be enabled for %s", tt.description)
				g.Expect(tt.arch).To(Equal(karpv1.ArchitectureAmd64), "Architecture should be AMD64 when artifact streaming is enabled")
				g.Expect([]string{
					customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
					customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
				}).To(ContainElement(tt.ossku), "OSSKU should be Ubuntu2204 or AzureLinux2 when artifact streaming is enabled")
			} else {
				g.Expect(actualEnabled).To(BeFalse(), "Artifact streaming should be disabled for %s", tt.description)
			}
		})
	}
}

func TestFIPSEnablement(t *testing.T) {
	baseBootstrapper := &customscriptsbootstrap.ProvisionClientBootstrap{
		ClusterName:                    "test-cluster",
		KubeletConfig:                  &bootstrap.KubeletConfiguration{MaxPods: int32(110)},
		SubnetID:                       "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
		Arch:                           karpv1.ArchitectureAmd64,
		SubscriptionID:                 "test-sub",
		ClusterResourceGroup:           "test-cluster-rg",
		ResourceGroup:                  "test-rg",
		KubeletClientTLSBootstrapToken: "testbtokenid.testbtokensecret",
		IsWindows:                      false,
		StorageProfile:                 consts.StorageProfileManagedDisks,
		NodeBootstrappingProvider:      &fake.NodeBootstrappingAPI{},
		InstanceType: &cloudprovider.InstanceType{
			Name: "Standard_D2s_v3",
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("2"),
				v1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}

	tests := []struct {
		name               string
		ossku              string
		kubernetesVersion  string
		imageDistro        string
		fipsMode           *v1beta1.FIPSMode
		expectedEnableFIPS bool
		description        string
	}{
		{
			name:               "FIPSMode FIPS Ubuntu2004 - EnableFIPS true",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUUbuntu2004,
			kubernetesVersion:  "1.31.0",
			imageDistro:        "aks-ubuntu-fips-containerd-20.04-gen2",
			fipsMode:           &v1beta1.FIPSModeFIPS,
			expectedEnableFIPS: true,
			description:        "FIPS should be enabled for Ubuntu2004 with FIPSMode FIPS",
		},
		{
			name:               "FIPSMode nil Ubuntu2204 - EnableFIPS false",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
			kubernetesVersion:  "1.31.0",
			imageDistro:        "aks-ubuntu-containerd-22.04-gen2",
			fipsMode:           nil,
			expectedEnableFIPS: false,
			description:        "FIPS should be disabled for Ubuntu2204 with FIPSMode nil",
		},
		{
			name:               "FIPSMode Disabled Ubuntu2204 - EnableFIPS false",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204,
			kubernetesVersion:  "1.31.0",
			imageDistro:        "aks-ubuntu-containerd-22.04-gen2",
			fipsMode:           &v1beta1.FIPSModeDisabled,
			expectedEnableFIPS: false,
			description:        "FIPS should be disabled for Ubuntu2204 with FIPSMode Disabled",
		},
		{
			name:               "FIPSMode FIPS AzureLinux2 - EnableFIPS true",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
			kubernetesVersion:  "1.31.0",
			imageDistro:        "aks-azurelinux-v2-gen2",
			fipsMode:           &v1beta1.FIPSModeFIPS,
			expectedEnableFIPS: true,
			description:        "FIPS should be enabled for AzureLinux2 with FIPSMode FIPS",
		},
		{
			name:               "FIPSMode nil AzureLinux2 - EnableFIPS false",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
			kubernetesVersion:  "1.31.0",
			imageDistro:        "aks-azurelinux-v2-gen2",
			fipsMode:           nil,
			expectedEnableFIPS: false,
			description:        "FIPS should be disabled for AzureLinux2 with FIPSMode nil",
		},
		{
			name:               "FIPSMode Disabled AzureLinux2 - EnableFIPS false",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2,
			kubernetesVersion:  "1.31.0",
			imageDistro:        "aks-azurelinux-v2-gen2",
			fipsMode:           &v1beta1.FIPSModeDisabled,
			expectedEnableFIPS: false,
			description:        "FIPS should be disabled for AzureLinux2 with FIPSMode Disabled",
		},
		{
			name:               "FIPSMode FIPS AzureLinux3 - EnableFIPS true",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
			kubernetesVersion:  "1.32.0",
			imageDistro:        "aks-azurelinux-v3-gen2",
			fipsMode:           &v1beta1.FIPSModeFIPS,
			expectedEnableFIPS: true,
			description:        "FIPS should be enabled for AzureLinux3 with FIPSMode FIPS",
		},
		{
			name:               "FIPSMode nil AzureLinux3 - EnableFIPS false",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
			kubernetesVersion:  "1.32.0",
			imageDistro:        "aks-azurelinux-v3-gen2",
			fipsMode:           nil,
			expectedEnableFIPS: false,
			description:        "FIPS should be disabled for AzureLinux3 with FIPSMode nil",
		},
		{
			name:               "FIPSMode Disabled AzureLinux3 - EnableFIPS false",
			ossku:              customscriptsbootstrap.ImageFamilyOSSKUAzureLinux3,
			kubernetesVersion:  "1.32.0",
			imageDistro:        "aks-azurelinux-v3-gen2",
			fipsMode:           &v1beta1.FIPSModeDisabled,
			expectedEnableFIPS: false,
			description:        "FIPS should be disabled for AzureLinux3 with FIPSMode Disabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			// Create a copy of the base bootstrapper and modify for this test
			bootstrapper := *baseBootstrapper
			bootstrapper.OSSKU = tt.ossku
			bootstrapper.KubernetesVersion = tt.kubernetesVersion
			bootstrapper.ImageDistro = tt.imageDistro
			bootstrapper.FIPSMode = tt.fipsMode

			// Setup context with options
			ctx := options.ToContext(context.Background(), &options.Options{
				VMMemoryOverheadPercent: 0.075,
				KubeletIdentityClientID: "test-kubelet-client-id",
			})

			values, err := bootstrapper.ConstructProvisionValues(ctx)

			// For all cases, expect success
			g.Expect(err).ToNot(HaveOccurred(), tt.description)
			g.Expect(values).ToNot(BeNil(), "ProvisionValues should not be nil")
			g.Expect(values.ProvisionProfile).ToNot(BeNil(), "ProvisionProfile should not be nil")

			g.Expect(values.ProvisionProfile.EnableFIPS).To(Equal(lo.ToPtr(tt.expectedEnableFIPS)),
				"FIPS enablement mismatch: %s. Expected: %t, Actual: %t",
				tt.description, tt.expectedEnableFIPS, *values.ProvisionProfile.EnableFIPS)
		})
	}
}
