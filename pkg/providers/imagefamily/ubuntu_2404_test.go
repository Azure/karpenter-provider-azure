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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/customscriptsbootstrap"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	. "github.com/onsi/gomega"
)

func TestUbuntu2404_Name(t *testing.T) {
	g := NewWithT(t)
	ubuntu := &imagefamily.Ubuntu2404{
		Options: &template.StaticParameters{},
	}
	g.Expect(ubuntu.Name()).To(Equal(v1beta1.Ubuntu2404ImageFamily))
}

func TestUbuntu2404_DefaultImages(t *testing.T) {
	ubuntu := &imagefamily.Ubuntu2404{
		Options: &template.StaticParameters{},
	}

	t.Run("should return correct default images", func(t *testing.T) {
		g := NewWithT(t)
		images := ubuntu.DefaultImages(false, nil)
		g.Expect(images).To(HaveLen(3))

		g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.Ubuntu2404Gen2ImageDefinition))
		g.Expect(images[0].Distro).To(Equal("aks-ubuntu-containerd-24.04-gen2"))

		g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.Ubuntu2404Gen1ImageDefinition))
		g.Expect(images[1].Distro).To(Equal("aks-ubuntu-containerd-24.04"))

		g.Expect(images[2].ImageDefinition).To(Equal(imagefamily.Ubuntu2404Gen2ArmImageDefinition))
		g.Expect(images[2].Distro).To(Equal("aks-ubuntu-arm64-containerd-24.04-gen2"))
	})

	t.Run("should return empty images for FIPS mode without SIG", func(t *testing.T) {
		g := NewWithT(t)
		fipsMode := v1beta1.FIPSModeFIPS
		images := ubuntu.DefaultImages(false, &fipsMode)
		g.Expect(images).To(BeEmpty())
	})

	t.Run("should return empty images for FIPS mode with SIG (not yet supported)", func(t *testing.T) {
		g := NewWithT(t)
		fipsMode := v1beta1.FIPSModeFIPS
		images := ubuntu.DefaultImages(true, &fipsMode)
		g.Expect(images).To(BeEmpty())
	})
}

func TestUbuntu2404_CustomScriptsNodeBootstrapping(t *testing.T) {
	g := NewWithT(t)
	ubuntu := &imagefamily.Ubuntu2404{
		Options: &template.StaticParameters{
			ClusterName:                    "test-cluster",
			ClusterEndpoint:                "https://test-cluster.hcp.westus2.azmk8s.io:443",
			KubeletIdentityClientID:        "test-client-id",
			TenantID:                       "test-tenant-id",
			SubscriptionID:                 "test-subscription-id",
			ResourceGroup:                  "test-resource-group",
			Location:                       "westus2",
			ClusterResourceGroup:           "test-cluster-resource-group",
			ClusterID:                      "test-cluster-id",
			APIServerName:                  "test-api-server",
			KubeletClientTLSBootstrapToken: "test-bootstrap-token",
			NetworkPlugin:                  "azure",
			NetworkPolicy:                  "none",
			KubernetesVersion:              "1.34.0",
			Arch:                           "amd64",
			SubnetID:                       "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/test/subnets/test",
		},
	}

	bootstrapper := ubuntu.CustomScriptsNodeBootstrapping(
		nil, nil, nil, nil, nil, "test-distro", "Standard_LRS", nil, nil, nil, nil,
	)
	provisionBootstrapper, ok := bootstrapper.(customscriptsbootstrap.ProvisionClientBootstrap)
	g.Expect(ok).To(BeTrue(), "Expected ProvisionClientBootstrap type")
	g.Expect(provisionBootstrapper.OSSKU).To(Equal(customscriptsbootstrap.ImageFamilyOSSKUUbuntu2404), "ImageFamily field must be set to prevent unsupported image family errors")
}
