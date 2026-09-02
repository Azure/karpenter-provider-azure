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

func TestAzureContainerLinux(t *testing.T) {
	family := &imagefamily.AzureContainerLinux{Options: &template.StaticParameters{}}

	t.Run("name", func(t *testing.T) {
		NewWithT(t).Expect(family.Name()).To(Equal(v1beta1.AzureContainerLinuxImageFamily))
	})

	t.Run("default images", func(t *testing.T) {
		images := family.DefaultImages(false, nil, true)
		g := NewWithT(t)
		g.Expect(images).To(HaveLen(2))
		g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2ImageDefinition))
		g.Expect(images[0].Distro).To(Equal("aks-acl-gen2-tl"))
		g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2ArmImageDefinition))
		g.Expect(images[1].Distro).To(Equal("aks-acl-arm64-gen2-tl"))
	})

	t.Run("no images without trusted launch", func(t *testing.T) {
		NewWithT(t).Expect(family.DefaultImages(false, nil, false)).To(BeEmpty())
	})

	t.Run("FIPS images", func(t *testing.T) {
		fips := v1beta1.FIPSModeFIPS
		images := family.DefaultImages(true, &fips, true)
		g := NewWithT(t)
		g.Expect(images).To(HaveLen(2))
		g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2FIPSImageDefinition))
		g.Expect(images[0].Distro).To(Equal("aks-acl-gen2-fips-tl"))
		g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2ArmFIPSImageDefinition))
		g.Expect(images[1].Distro).To(Equal("aks-acl-arm64-gen2-fips-tl"))
	})

	t.Run("scriptless bootstrap is unsupported", func(t *testing.T) {
		_, err := family.ScriptlessCustomData(nil, nil, nil, nil, nil).Script()
		NewWithT(t).Expect(err).To(MatchError(ContainSubstring("requires bootstrappingclient or an AKS Machine API provision mode")))
	})
}

func TestAzureContainerLinuxCustomScriptsNodeBootstrapping(t *testing.T) {
	family := &imagefamily.AzureContainerLinux{Options: &template.StaticParameters{}}
	bootstrapper := family.CustomScriptsNodeBootstrapping(nil, nil, nil, nil, nil, "aks-acl-gen2-tl", "Managed", nil, nil, nil, nil, nil, nil, nil)
	provisionBootstrapper, ok := bootstrapper.(customscriptsbootstrap.ProvisionClientBootstrap)
	g := NewWithT(t)
	g.Expect(ok).To(BeTrue())
	g.Expect(provisionBootstrapper.OSSKU).To(Equal(customscriptsbootstrap.ImageFamilyOSSKUAzureContainerLinux))
}
