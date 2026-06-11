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
	for _, fam := range []string{
		v1beta1.Windows2019ImageFamily,
		v1beta1.Windows2022ImageFamily,
		v1beta1.Windows2025ImageFamily,
		v1beta1.WindowsAnnualImageFamily,
	} {
		w := imagefamily.Windows{Family: fam}
		g.Expect(w.Name()).To(Equal(fam))
	}
}

func TestWindows_DefaultImages(t *testing.T) {
	g := NewWithT(t)

	// Windows2022 (SIG) returns gen2 first, then gen1, all in the AKSWindows gallery.
	images := imagefamily.Windows{Family: v1beta1.Windows2022ImageFamily}.DefaultImages(true, nil)
	g.Expect(images).To(HaveLen(2))

	g.Expect(images[0].GalleryName).To(Equal(imagefamily.AKSWindowsGalleryName))
	g.Expect(images[0].GalleryResourceGroup).To(Equal(imagefamily.AKSWindowsResourceGroup))
	g.Expect(images[0].ImageDefinition).To(Equal(imagefamily.Windows2022ContainerdGen2ImageDefinition))
	g.Expect(images[0].Distro).To(Equal("aks-windows-2022-containerd-gen2"))
	// gen2 image must require HyperV gen2 and amd64
	g.Expect(images[0].Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Has(v1beta1.HyperVGenerationV2)).To(BeTrue())

	g.Expect(images[1].ImageDefinition).To(Equal(imagefamily.Windows2022ContainerdImageDefinition))
	g.Expect(images[1].Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Has(v1beta1.HyperVGenerationV1)).To(BeTrue())

	// Windows2019 only publishes a gen1 containerd image.
	images2019 := imagefamily.Windows{Family: v1beta1.Windows2019ImageFamily}.DefaultImages(true, nil)
	g.Expect(images2019).To(HaveLen(1))
	g.Expect(images2019[0].ImageDefinition).To(Equal(imagefamily.Windows2019ContainerdImageDefinition))

	// Windows is SIG-only: CIG (useSIG=false) yields no images.
	g.Expect(imagefamily.Windows{Family: v1beta1.Windows2022ImageFamily}.DefaultImages(false, nil)).To(BeEmpty())

	// FIPS is not available for Windows.
	g.Expect(imagefamily.Windows{Family: v1beta1.Windows2022ImageFamily}.DefaultImages(true, lo.ToPtr(v1beta1.FIPSModeFIPS))).To(BeEmpty())
}

func TestWindows_GetImageFamily(t *testing.T) {
	g := NewWithT(t)
	for _, fam := range []string{
		v1beta1.Windows2019ImageFamily,
		v1beta1.Windows2022ImageFamily,
		v1beta1.Windows2025ImageFamily,
		v1beta1.WindowsAnnualImageFamily,
	} {
		resolved := imagefamily.GetImageFamily(lo.ToPtr(fam), nil, "1.30.0", nil)
		w, ok := resolved.(*imagefamily.Windows)
		g.Expect(ok).To(BeTrue(), "GetImageFamily(%s) should return *Windows", fam)
		g.Expect(w.Family).To(Equal(fam))
	}
}

func TestWindows_BootstrapMethodsUnsupported(t *testing.T) {
	g := NewWithT(t)
	w := imagefamily.Windows{Family: v1beta1.Windows2022ImageFamily}

	// Both bootstrap paths are only valid for non-Windows; for Windows they must error
	// (Windows is provisioned via the AKS Machine API path, not these bootstrappers).
	_, err := w.ScriptlessCustomData(nil, nil, nil, nil, nil).Script()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("aksmachineapi"))

	_, _, err = w.CustomScriptsNodeBootstrapping(nil, nil, nil, nil, nil, "", "", nil, nil, nil, nil, nil).GetCustomDataAndCSE(context.Background())
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("aksmachineapi"))
}
