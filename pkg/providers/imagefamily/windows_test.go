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
	"context"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func TestWindows_Name(t *testing.T) {
	g := NewWithT(t)
	g.Expect(imagefamily.Windows2022{}.Name()).To(Equal(v1beta1.Windows2022ImageFamily))
	g.Expect(imagefamily.Windows2025{}.Name()).To(Equal(v1beta1.Windows2025ImageFamily))
}

func TestWindows_DefaultImages(t *testing.T) {
	g := NewWithT(t)

	// Windows2022 (SIG) returns gen2 first, then gen1, all in the AKSWindows gallery.
	images := imagefamily.Windows2022{}.DefaultImages(true, nil, false)
	g.Expect(images).To(HaveLen(2))

	g.Expect(images[0].GalleryName).To(Equal(imagefamily.AKSWindowsGalleryName))
	g.Expect(images[0].GalleryResourceGroup).To(Equal(imagefamily.AKSWindowsResourceGroup))
	g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.Windows2022ContainerdGen2ImageDefinition))
	g.Expect(images[0].Distro).To(Equal("aks-windows-2022-containerd-gen2"))
	// gen2 image must require HyperV gen2 and amd64
	g.Expect(images[0].Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Has(v1beta1.HyperVGenerationV2)).To(BeTrue())

	g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.Windows2022ContainerdImageDefinition))
	g.Expect(images[1].Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Has(v1beta1.HyperVGenerationV1)).To(BeTrue())

	// Windows2025 (SIG) returns gen2 first, then gen1, all in the AKSWindows gallery.
	images2025 := imagefamily.Windows2025{}.DefaultImages(true, nil, false)
	g.Expect(images2025).To(HaveLen(2))
	g.Expect(images2025[0].ImageDefinition).To(Equal(imagefamily.Windows2025Gen2ImageDefinition))
	g.Expect(images2025[1].ImageDefinition).To(Equal(imagefamily.Windows2025ImageDefinition))

	// Windows is SIG-only: CIG (useSIG=false) yields no images.
	g.Expect(imagefamily.Windows2022{}.DefaultImages(false, nil, false)).To(BeEmpty())

	// Windows2022 does not support FIPS.
	g.Expect(imagefamily.Windows2022{}.DefaultImages(true, lo.ToPtr(v1beta1.FIPSModeFIPS), false)).To(BeEmpty())

	// Windows2025 requires FIPS in the AKS RP, so its images remain available in FIPS mode.
	g.Expect(imagefamily.Windows2025{}.DefaultImages(true, lo.ToPtr(v1beta1.FIPSModeFIPS), false)).To(HaveLen(2))

	trustedLaunchImages := imagefamily.Windows2025{}.DefaultImages(true, nil, true)
	g.Expect(trustedLaunchImages).To(HaveLen(1))
	g.Expect(trustedLaunchImages[0].ImageDefinition).To(Equal(imagefamily.Windows2025Gen2TLImageDefinition))
	g.Expect(trustedLaunchImages[0].Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Has(v1beta1.HyperVGenerationV2)).To(BeTrue())

	g.Expect(imagefamily.Windows2022{}.DefaultImages(true, nil, true)).To(BeEmpty())
}

func TestWindows_GetImageFamily(t *testing.T) {
	g := NewWithT(t)
	resolved2022 := imagefamily.GetImageFamily(lo.ToPtr(v1beta1.Windows2022ImageFamily), nil, false, "1.30.0", nil)
	_, ok := resolved2022.(*imagefamily.Windows2022)
	g.Expect(ok).To(BeTrue())

	resolved2025 := imagefamily.GetImageFamily(lo.ToPtr(v1beta1.Windows2025ImageFamily), nil, false, "1.30.0", nil)
	_, ok = resolved2025.(*imagefamily.Windows2025)
	g.Expect(ok).To(BeTrue())
}

func TestWindows_BootstrapMethodsUnsupported(t *testing.T) {
	g := NewWithT(t)
	w := imagefamily.Windows2022{}

	// Both bootstrap paths are only valid for non-Windows; for Windows they must error
	// (Windows is provisioned via the AKS Machine API path, not these bootstrappers).
	_, err := w.ScriptlessCustomData(nil, nil, nil, nil, nil).Script()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("aksmachineapi"))

	_, _, err = w.CustomScriptsNodeBootstrapping(nil, nil, nil, nil, nil, "", "", nil, nil, nil, nil, nil, nil, nil).GetCustomDataAndCSE(context.Background())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("aksmachineapi"))
}
