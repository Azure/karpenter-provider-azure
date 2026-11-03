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
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestAzureContainerLinux(t *testing.T) {
	family := &imagefamily.AzureContainerLinux{Options: &template.StaticParameters{}}

	t.Run("name", func(t *testing.T) {
		NewWithT(t).Expect(family.Name()).To(Equal(v1beta1.AzureContainerLinuxImageFamily))
	})

	t.Run("default images", func(t *testing.T) {
		images := family.DefaultImages(true, nil, true, false)
		g := NewWithT(t)
		g.Expect(images).To(HaveLen(2))
		g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2ImageDefinition))
		g.Expect(images[0].Distro).To(Equal("aks-acl-gen2-tl"))
		g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2ArmImageDefinition))
		g.Expect(images[1].Distro).To(Equal("aks-acl-arm64-gen2-tl"))
		g.Expect(images[0].Requirements.Get(v1.LabelArchStable).Values()).To(ConsistOf(karpv1.ArchitectureAmd64))
		g.Expect(images[1].Requirements.Get(v1.LabelArchStable).Values()).To(ConsistOf(karpv1.ArchitectureArm64))
		for _, image := range images {
			g.Expect(image.GalleryName).To(Equal("AKSAzureLinux"))
			g.Expect(image.GalleryResourceGroup).To(Equal("AKS-AzureLinux"))
			g.Expect(image.Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1beta1.HyperVGenerationV2))
		}
	})

	t.Run("FIPS images", func(t *testing.T) {
		fips := v1beta1.FIPSModeFIPS
		images := family.DefaultImages(true, &fips, true, false)
		g := NewWithT(t)
		g.Expect(images).To(HaveLen(2))
		g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2FIPSImageDefinition))
		g.Expect(images[0].Distro).To(Equal("aks-acl-gen2-fips-tl"))
		g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.AzureContainerLinuxGen2ArmFIPSImageDefinition))
		g.Expect(images[1].Distro).To(Equal("aks-acl-arm64-gen2-fips-tl"))
	})

	for _, fips := range []*v1beta1.FIPSMode{nil, &v1beta1.FIPSModeDisabled, &v1beta1.FIPSModeFIPS} {
		t.Run("requires Trusted Launch", func(t *testing.T) {
			NewWithT(t).Expect(family.DefaultImages(true, fips, false, false)).To(BeEmpty())
		})
		t.Run("does not support Kata", func(t *testing.T) {
			NewWithT(t).Expect(family.DefaultImages(true, fips, true, true)).To(BeEmpty())
		})
	}

	t.Run("scriptless bootstrap is unsupported", func(t *testing.T) {
		_, err := family.ScriptlessCustomData(nil, nil, nil, nil, nil).Script()
		NewWithT(t).Expect(err).To(MatchError(ContainSubstring("requires an AKS Machine API provision mode")))
	})

	t.Run("custom scripts bootstrap is unsupported", func(t *testing.T) {
		_, _, err := family.CustomScriptsNodeBootstrapping(nil, nil, nil, nil, nil, "", "", nil, nil, nil, nil, nil, nil, nil, nil).GetCustomDataAndCSE(t.Context())
		NewWithT(t).Expect(err).To(MatchError(ContainSubstring("requires an AKS Machine API provision mode")))
	})
}
