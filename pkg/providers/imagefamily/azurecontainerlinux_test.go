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
	"fmt"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	template "github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate/parameters"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func TestAzureContainerLinux(t *testing.T) {
	family := &imagefamily.AzureContainerLinux{Options: &template.StaticParameters{}}

	t.Run("name", func(t *testing.T) {
		NewWithT(t).Expect(family.Name()).To(Equal(v1beta1.AzureContainerLinuxImageFamily))
	})

	type expectedImage struct {
		definition, distro, architecture, confidentialComputingType string
	}
	defaultImages := []expectedImage{
		{"aclgen2TL", "aks-acl-gen2-tl", karpv1.ArchitectureAmd64, "None"},
		{"aclgen2arm64TL", "aks-acl-arm64-gen2-tl", karpv1.ArchitectureArm64, "None"},
		{"aclgen2CVM", "aks-acl-cvm-gen2", karpv1.ArchitectureAmd64, "SNP"},
	}
	fipsImages := []expectedImage{
		{"aclgen2fipsTL", "aks-acl-gen2-fips-tl", karpv1.ArchitectureAmd64, "None"},
		{"aclgen2arm64fipsTL", "aks-acl-arm64-gen2-fips-tl", karpv1.ArchitectureArm64, "None"},
		{"aclgen2fipsCVM", "aks-acl-cvm-gen2-fips", karpv1.ArchitectureAmd64, "SNP"},
	}
	for _, tc := range []struct {
		name string
		fips *v1beta1.FIPSMode
		kata bool
		want []expectedImage
	}{
		{"default", nil, false, defaultImages},
		{"FIPS disabled", &v1beta1.FIPSModeDisabled, false, defaultImages},
		{"FIPS", &v1beta1.FIPSModeFIPS, false, fipsImages},
		{"Kata", nil, true, nil},
		{"Kata with FIPS disabled", &v1beta1.FIPSModeDisabled, true, nil},
		{"Kata with FIPS", &v1beta1.FIPSModeFIPS, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			images := family.DefaultImages(true, tc.fips, true, tc.kata)
			g.Expect(images).To(HaveLen(len(tc.want)))
			compatibleImages := map[[3]string][]string{}
			for i, want := range tc.want {
				compatibleImages[[3]string{want.architecture, v1beta1.HyperVGenerationV2, want.confidentialComputingType}] = []string{want.definition}
				image := images[i]
				g.Expect(image.ImageDefinition).To(Equal(want.definition))
				g.Expect(image.Distro).To(Equal(want.distro))
				g.Expect(image.GalleryName).To(Equal("AKSAzureLinux"))
				g.Expect(image.GalleryResourceGroup).To(Equal("AKS-AzureLinux"))
				g.Expect(image.Requirements.Get(v1.LabelArchStable).Values()).To(ConsistOf(want.architecture))
				g.Expect(image.Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1beta1.HyperVGenerationV2))
				g.Expect(image.Requirements.Get(v1beta1.LabelSKUConfidentialComputingType).Values()).To(ConsistOf(want.confidentialComputingType))
			}
			g.Expect(family.DefaultImages(false, tc.fips, false, tc.kata)).To(BeEmpty())
			g.Expect(family.DefaultImages(false, tc.fips, true, tc.kata)).To(BeEmpty())
			g.Expect(family.DefaultImages(true, tc.fips, false, tc.kata)).To(BeEmpty())
			for _, architecture := range []string{karpv1.ArchitectureAmd64, karpv1.ArchitectureArm64} {
				for _, generation := range []string{v1beta1.HyperVGenerationV1, v1beta1.HyperVGenerationV2} {
					for _, confidentialComputingType := range []string{"None", "SNP"} {
						t.Run(fmt.Sprintf("%s/gen%s/%s", architecture, generation, confidentialComputingType), func(t *testing.T) {
							requirements := scheduling.NewRequirements(
								scheduling.NewRequirement(v1.LabelArchStable, v1.NodeSelectorOpIn, architecture),
								scheduling.NewRequirement(v1beta1.LabelSKUHyperVGeneration, v1.NodeSelectorOpIn, generation),
								scheduling.NewRequirement(v1beta1.LabelSKUConfidentialComputingType, v1.NodeSelectorOpIn, confidentialComputingType),
							)
							var matches []string
							for _, image := range images {
								if requirements.Compatible(image.Requirements, v1beta1.AllowUndefinedWellKnownAndRestrictedLabels) == nil {
									matches = append(matches, image.ImageDefinition)
								}
							}
							expected := compatibleImages[[3]string{architecture, generation, confidentialComputingType}]
							NewWithT(t).Expect(matches).To(Equal(expected))
						})
					}
				}
			}
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
