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
	"github.com/stretchr/testify/assert"
)

func TestUbuntu2404_Name(t *testing.T) {
	ubuntu := &imagefamily.Ubuntu2404{
		Options: &template.StaticParameters{},
	}
	assert.Equal(t, v1beta1.Ubuntu2404ImageFamily, ubuntu.Name())
}

func TestUbuntu2404_DefaultImages(t *testing.T) {
	ubuntu := &imagefamily.Ubuntu2404{
		Options: &template.StaticParameters{},
	}

	t.Run("should return correct default images", func(t *testing.T) {
		images := ubuntu.DefaultImages(false, nil)
		assert.Len(t, images, 3)

		assert.Equal(t, imagefamily.Ubuntu2404Gen2ImageDefinition, images[0].ImageDefinition)
		assert.Equal(t, "aks-ubuntu-containerd-24.04-gen2", images[0].Distro)

		assert.Equal(t, imagefamily.Ubuntu2404Gen1ImageDefinition, images[1].ImageDefinition)
		assert.Equal(t, "aks-ubuntu-containerd-24.04", images[1].Distro)

		assert.Equal(t, imagefamily.Ubuntu2404Gen2ArmImageDefinition, images[2].ImageDefinition)
		assert.Equal(t, "aks-ubuntu-arm64-containerd-24.04-gen2", images[2].Distro)
	})

	t.Run("should return empty images for FIPS mode without SIG", func(t *testing.T) {
		fipsMode := v1beta1.FIPSModeFIPS
		images := ubuntu.DefaultImages(false, &fipsMode)
		assert.Empty(t, images)
	})

	t.Run("should return empty images for FIPS mode with SIG (not yet supported)", func(t *testing.T) {
		fipsMode := v1beta1.FIPSModeFIPS
		images := ubuntu.DefaultImages(true, &fipsMode)
		assert.Empty(t, images)
	})
}

func TestUbuntu2404_CustomScriptsNodeBootstrapping(t *testing.T) {
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
		nil, nil, nil, nil, nil, "test-distro", "Standard_LRS", nil, nil, nil, // artifactStreamingEnabled
	)
	provisionBootstrapper, ok := bootstrapper.(customscriptsbootstrap.ProvisionClientBootstrap)
	assert.True(t, ok, "Expected ProvisionClientBootstrap type")
	assert.Equal(t, customscriptsbootstrap.ImageFamilyOSSKUUbuntu2404, provisionBootstrapper.OSSKU, "ImageFamily field must be set to prevent unsupported image family errors")
}
