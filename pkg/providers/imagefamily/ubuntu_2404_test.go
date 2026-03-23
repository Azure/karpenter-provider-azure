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
	. "github.com/onsi/gomega"
)

func TestUbuntu2404_Name(t *testing.T) {
	g := NewWithT(t)
	ubuntu := &imagefamily.Ubuntu2404{}
	g.Expect(ubuntu.Name()).To(Equal(v1beta1.Ubuntu2404ImageFamily))
}

func TestUbuntu2404_DefaultImages(t *testing.T) {
	ubuntu := &imagefamily.Ubuntu2404{}

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
