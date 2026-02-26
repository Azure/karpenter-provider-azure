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
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestUbuntu2204_CustomScriptsNodeBootstrapping(t *testing.T) {
	ubuntu := imagefamily.Ubuntu2204{
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
	imageDistro := "aks-ubuntu-containerd-22.04-gen2"
	storageProfile := "ManagedDisks"
	nodeBootstrappingClient := &fake.NodeBootstrappingAPI{}

	// Note: FIPSMode test scenarios are distributed across image families rather than comprehensively tested in each.
	// While not perfect since each family has its own method, the test cases are extremely simple, and this keeps things simple
	fipsMode := lo.ToPtr(v1beta1.FIPSModeDisabled)
	localDNS := &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
	artifactStreaming := &v1beta1.ArtifactStreamingSettings{Mode: v1beta1.ArtifactStreamingModeDisabled}

	bootstrapper := ubuntu.CustomScriptsNodeBootstrapping(
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
	)

	g := NewWithT(t)

	// Verify the returned bootstrapper is of the correct type
	provisionBootstrapper, ok := bootstrapper.(customscriptsbootstrap.ProvisionClientBootstrap)
	g.Expect(ok).To(BeTrue(), "Expected customscriptsbootstrap.ProvisionClientBootstrap type")

	// Verify all fields are properly set
	g.Expect(provisionBootstrapper.ClusterName).To(Equal("test-cluster"))
	g.Expect(provisionBootstrapper.KubeletConfig).To(Equal(kubeletConfig))
	g.Expect(provisionBootstrapper.Taints).To(Equal(taints))
	g.Expect(provisionBootstrapper.StartupTaints).To(Equal(startupTaints))
	g.Expect(provisionBootstrapper.Labels).To(Equal(labels))
	g.Expect(provisionBootstrapper.SubnetID).To(Equal("/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"))
	g.Expect(provisionBootstrapper.Arch).To(Equal(karpv1.ArchitectureAmd64))
	g.Expect(provisionBootstrapper.SubscriptionID).To(Equal("test-subscription"))
	g.Expect(provisionBootstrapper.ResourceGroup).To(Equal("test-rg"))
	g.Expect(provisionBootstrapper.ClusterResourceGroup).To(Equal("test-cluster-rg"))
	g.Expect(provisionBootstrapper.KubeletClientTLSBootstrapToken).To(Equal("test-token"))
	g.Expect(provisionBootstrapper.KubernetesVersion).To(Equal("1.31.0"))
	g.Expect(provisionBootstrapper.ImageDistro).To(Equal(imageDistro))
	g.Expect(provisionBootstrapper.InstanceType).To(Equal(instanceType))
	g.Expect(provisionBootstrapper.StorageProfile).To(Equal(storageProfile))
	g.Expect(provisionBootstrapper.NodeBootstrappingProvider).To(Equal(nodeBootstrappingClient))
	g.Expect(provisionBootstrapper.OSSKU).To(Equal(customscriptsbootstrap.ImageFamilyOSSKUUbuntu2204), "ImageFamily field must be set to prevent unsupported image family errors")
	g.Expect(provisionBootstrapper.FIPSMode).To(Equal(fipsMode), "FIPSMode field must match the input parameter")
	g.Expect(provisionBootstrapper.LocalDNSProfile).To(Equal(localDNS), "LocalDNSProfile field must match the input parameter")
}

func TestUbuntu2204_Name(t *testing.T) {
	g := NewWithT(t)
	ubuntu := imagefamily.Ubuntu2204{}
	g.Expect(ubuntu.Name()).To(Equal(v1beta1.Ubuntu2204ImageFamily))
}
