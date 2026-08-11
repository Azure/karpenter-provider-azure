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
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestAzureContainerLinux_CustomScriptsNodeBootstrapping(t *testing.T) {
	acl := imagefamily.AzureContainerLinux{
		Options: &parameters.StaticParameters{
			ClusterName:                    "test-cluster",
			SubnetID:                       "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
			Arch:                           karpv1.ArchitectureAmd64,
			SubscriptionID:                 "test-subscription",
			ResourceGroup:                  "test-rg",
			ClusterResourceGroup:           "test-cluster-rg",
			KubeletClientTLSBootstrapToken: "test-token",
			KubernetesVersion:              "1.34.0",
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
	imageDistro := "aks-acl-gen2-tl"
	storageProfile := "ManagedDisks"
	nodeBootstrappingClient := &fake.NodeBootstrappingAPI{}

	var fipsMode *v1beta1.FIPSMode
	var localDNS *v1beta1.LocalDNS
	var artifactStreaming *v1beta1.ArtifactStreaming
	var linuxOSConfig *v1beta1.LinuxOSConfiguration

	bootstrapper := acl.CustomScriptsNodeBootstrapping(
		kubeletConfig,
		taints,
		startupTaints,
		labels,
		instanceType,
		imageDistro,
		storageProfile,
		nodeBootstrappingClient,
		fipsMode,
		localDNS,
		artifactStreaming,
		linuxOSConfig,
	)

	g := NewWithT(t)
	provisionBootstrapper, ok := bootstrapper.(customscriptsbootstrap.ProvisionClientBootstrap)
	g.Expect(ok).To(BeTrue(), "Expected customscriptsbootstrap.ProvisionClientBootstrap type")
	g.Expect(provisionBootstrapper.OSSKU).To(Equal(customscriptsbootstrap.ImageFamilyOSSKUAzureContainerLinux))
}

func TestAzureContainerLinux_Name(t *testing.T) {
	g := NewWithT(t)
	acl := imagefamily.AzureContainerLinux{}
	g.Expect(acl.Name()).To(Equal(v1beta1.AzureContainerLinuxImageFamily))
}
