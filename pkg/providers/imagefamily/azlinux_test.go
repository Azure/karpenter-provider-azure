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

package imagefamily_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestAzureLinux_CustomScriptsNodeBootstrapping(t *testing.T) {
	azureLinux := imagefamily.AzureLinux{
		Options: &parameters.StaticParameters{
			ClusterName:                    "test-cluster",
			SubnetID:                       "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			Arch:                           karpv1.ArchitectureAmd64,
			SubscriptionID:                 "test-subscription",
			ResourceGroup:                  "test-rg",
			ClusterResourceGroup:           "test-cluster-rg",
			KubeletClientTLSBootstrapToken: "test-token",
			KubernetesVersion:              "1.31.0",
		},
	}

	kubeletConfig := &bootstrap.KubeletConfiguration{MaxPods: int32(110)}
	taints := []v1.Taint{{Key: "test", Value: "value", Effect: v1.TaintEffectNoSchedule}}
	startupTaints := []v1.Taint{{Key: "startup", Value: "value", Effect: v1.TaintEffectNoSchedule}}
	labels := map[string]string{"test-label": "test-value"}
	instanceType := &cloudprovider.InstanceType{
		Name: "Standard_D2s_v3",
		Capacity: v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse("2"),
			v1.ResourceMemory: resource.MustParse("8Gi"),
		},
	}
	imageDistro := "aks-azurelinux-v2-gen2"
	storageProfile := "ManagedDisks"
	nodeBootstrappingClient := &fake.NodeBootstrappingAPI{}

	bootstrapper := azureLinux.CustomScriptsNodeBootstrapping(
		kubeletConfig,
		taints,
		startupTaints,
		labels,
		instanceType,
		imageDistro,
		storageProfile,
		nodeBootstrappingClient,
	)

	// Verify the returned bootstrapper is of the correct type
	provisionBootstrapper, ok := bootstrapper.(customscriptsbootstrap.ProvisionClientBootstrap)
	assert.True(t, ok, "Expected customscriptsbootstrap.ProvisionClientBootstrap type")

	// Verify all fields are properly set
	assert.Equal(t, "test-cluster", provisionBootstrapper.ClusterName)
	assert.Equal(t, kubeletConfig, provisionBootstrapper.KubeletConfig)
	assert.Equal(t, taints, provisionBootstrapper.Taints)
	assert.Equal(t, startupTaints, provisionBootstrapper.StartupTaints)
	assert.Equal(t, labels, provisionBootstrapper.Labels)
	assert.Equal(t, "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", provisionBootstrapper.SubnetID)
	assert.Equal(t, karpv1.ArchitectureAmd64, provisionBootstrapper.Arch)
	assert.Equal(t, "test-subscription", provisionBootstrapper.SubscriptionID)
	assert.Equal(t, "test-rg", provisionBootstrapper.ResourceGroup)
	assert.Equal(t, "test-cluster-rg", provisionBootstrapper.ClusterResourceGroup)
	assert.Equal(t, "test-token", provisionBootstrapper.KubeletClientTLSBootstrapToken)
	assert.Equal(t, "1.31.0", provisionBootstrapper.KubernetesVersion)
	assert.Equal(t, imageDistro, provisionBootstrapper.ImageDistro)
	assert.Equal(t, instanceType, provisionBootstrapper.InstanceType)
	assert.Equal(t, storageProfile, provisionBootstrapper.StorageProfile)
	assert.Equal(t, nodeBootstrappingClient, provisionBootstrapper.NodeBootstrappingProvider)
	assert.Equal(t, customscriptsbootstrap.ImageFamilyOSSKUAzureLinux2, provisionBootstrapper.OSSKU, "ImageFamily field must be set to prevent unsupported image family errors")
}

func TestAzureLinux_Name(t *testing.T) {
	azureLinux := imagefamily.AzureLinux{}
	assert.Equal(t, v1beta1.AzureLinuxImageFamily, azureLinux.Name())
}
