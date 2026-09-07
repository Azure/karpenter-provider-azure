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
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

func TestValidateImageFamilyCompatibility(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		imageFamily       *string
		fipsMode          *v1beta1.FIPSMode
		trustedLaunch     bool
		kubernetesVersion string
		wantErr           []string
	}{
		// Explicitly pinned Ubuntu2204.
		"explicit ubuntu2204 rejects versions below lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.25.1",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2204ImageFamily, "1.25.1", "supported range is >= 1.25.2 and < 1.37.0"},
		},
		"explicit ubuntu2204 accepts lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.25.2",
		},
		"explicit ubuntu2204 accepts below upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.36.9",
		},
		"explicit ubuntu2204 rejects upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.37.0",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2204ImageFamily, "1.37.0", "supported range is >= 1.25.2 and < 1.37.0"},
		},
		"explicit ubuntu2204 with trusted launch rejects upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			trustedLaunch:     true,
			kubernetesVersion: "1.37.0",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2204ImageFamily, "1.37.0", "supported range is >= 1.25.2 and < 1.37.0"},
		},
		"explicit ubuntu2204 with fips accepts below extended upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			kubernetesVersion: "1.38.9",
		},
		"explicit ubuntu2204 with fips rejects extended upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			kubernetesVersion: "1.39.0",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2204ImageFamily, "1.39.0", "with FIPS", "supported range is >= 1.25.2 and < 1.39.0"},
		},
		"explicit ubuntu2204 with fips and trusted launch rejects extended upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			trustedLaunch:     true,
			kubernetesVersion: "1.39.0",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2204ImageFamily, "1.39.0", "with FIPS", "supported range is >= 1.25.2 and < 1.39.0"},
		},

		// Explicitly pinned Ubuntu2404.
		"explicit ubuntu2404 rejects versions below lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.31.9",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2404ImageFamily, "1.31.9", "supported range is >= 1.32.0"},
		},
		"explicit ubuntu2404 accepts lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.32.0",
		},
		"explicit ubuntu2404 has no upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.39.0",
		},

		// Generic and unset Ubuntu are out of scope: what they resolve to is the
		// resolver's (or the AKS RP's) decision, and it stays valid across versions.
		"generic ubuntu is unrestricted below the ubuntu2204 lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			kubernetesVersion: "1.25.1",
		},
		"generic ubuntu is unrestricted at the ubuntu2204 upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			kubernetesVersion: "1.37.0",
		},
		"generic ubuntu with trusted launch is unrestricted at the ubuntu2204 upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			trustedLaunch:     true,
			kubernetesVersion: "1.37.0",
		},
		"generic ubuntu with trusted launch is unrestricted well past the ubuntu2204 upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			trustedLaunch:     true,
			kubernetesVersion: "1.40.0",
		},
		"generic ubuntu with fips is unrestricted at the fips upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			kubernetesVersion: "1.39.0",
		},
		"generic ubuntu with fips and trusted launch is unrestricted at the fips upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			trustedLaunch:     true,
			kubernetesVersion: "1.39.0",
		},
		"unset image family is unrestricted below the ubuntu2204 lower bound": {
			kubernetesVersion: "1.25.1",
		},
		"unset image family is unrestricted at the ubuntu2204 upper bound": {
			kubernetesVersion: "1.37.0",
		},
		"unset image family with trusted launch is unrestricted at the ubuntu2204 upper bound": {
			trustedLaunch:     true,
			kubernetesVersion: "1.37.0",
		},
		"unset image family with fips and trusted launch is unrestricted at the fips upper bound": {
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			trustedLaunch:     true,
			kubernetesVersion: "1.39.0",
		},
		"unset image family is unrestricted with an unparsable kubernetes version": {
			kubernetesVersion: "1.32.x",
		},

		// Other families are out of scope entirely.
		"azlinux remains unrestricted": {
			imageFamily:       lo.ToPtr(v1beta1.AzureLinuxImageFamily),
			kubernetesVersion: "1.20.0",
		},

		// Version parsing.
		"accepts tolerant two segment versions": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.32",
		},
		"rejects tolerant two segment versions below the lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.31",
			wantErr:           []string{"requested image family", v1beta1.Ubuntu2404ImageFamily, "1.31", "supported range is >= 1.32.0"},
		},
		"treats prerelease upper bound consistently with semver": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.37.0-beta.1",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)
			nodeClass := &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: tc.imageFamily,
					FIPSMode:    tc.fipsMode,
				},
			}
			if tc.trustedLaunch {
				nodeClass.Spec.Security = &v1beta1.Security{
					TrustedLaunch: &v1beta1.TrustedLaunch{
						VTPM: lo.ToPtr(true),
					},
				}
			}

			err := imagefamily.ValidateImageFamilyCompatibility(nodeClass, tc.kubernetesVersion)
			if len(tc.wantErr) == 0 {
				g.Expect(err).ToNot(HaveOccurred())
				return
			}

			g.Expect(err).To(HaveOccurred())
			for _, substring := range tc.wantErr {
				g.Expect(err.Error()).To(ContainSubstring(substring))
			}

			var incompatibleErr *imagefamily.ImageFamilyKubernetesVersionIncompatibleError
			g.Expect(errors.As(err, &incompatibleErr)).To(BeTrue())
			g.Expect(incompatibleErr.RequestedImageFamily).To(Equal(lo.FromPtr(tc.imageFamily)))
			g.Expect(incompatibleErr.KubernetesVersion).To(Equal(tc.kubernetesVersion))
		})
	}

	t.Run("reports the applied bounds on the typed incompatibility error", func(t *testing.T) {
		t.Parallel()

		g := NewWithT(t)
		nodeClass := &v1beta1.AKSNodeClass{
			Spec: v1beta1.AKSNodeClassSpec{
				ImageFamily: lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
				FIPSMode:    lo.ToPtr(v1beta1.FIPSModeFIPS),
			},
		}

		err := imagefamily.ValidateImageFamilyCompatibility(nodeClass, "1.39.0")
		var incompatibleErr *imagefamily.ImageFamilyKubernetesVersionIncompatibleError
		g.Expect(errors.As(err, &incompatibleErr)).To(BeTrue())
		g.Expect(incompatibleErr.FIPS).To(BeTrue())
		g.Expect(incompatibleErr.MinimumVersion.String()).To(Equal("1.25.2"))
		g.Expect(incompatibleErr.MaximumVersion).ToNot(BeNil())
		g.Expect(incompatibleErr.MaximumVersion.String()).To(Equal("1.39.0"))

		nodeClass = &v1beta1.AKSNodeClass{
			Spec: v1beta1.AKSNodeClassSpec{
				ImageFamily: lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			},
		}

		err = imagefamily.ValidateImageFamilyCompatibility(nodeClass, "1.31.0")
		g.Expect(errors.As(err, &incompatibleErr)).To(BeTrue())
		g.Expect(incompatibleErr.FIPS).To(BeFalse())
		g.Expect(incompatibleErr.MinimumVersion.String()).To(Equal("1.32.0"))
		g.Expect(incompatibleErr.MaximumVersion).To(BeNil())
	})

	t.Run("returns an error for malformed kubernetes version", func(t *testing.T) {
		t.Parallel()

		g := NewWithT(t)
		nodeClass := &v1beta1.AKSNodeClass{
			Spec: v1beta1.AKSNodeClassSpec{
				ImageFamily: lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			},
		}

		err := imagefamily.ValidateImageFamilyCompatibility(nodeClass, "1.32.x")
		g.Expect(err).To(MatchError(ContainSubstring(`malformed discovered Kubernetes version "1.32.x": expected a semantic version like 1.32.0`)))

		var incompatibleErr *imagefamily.ImageFamilyKubernetesVersionIncompatibleError
		g.Expect(errors.As(err, &incompatibleErr)).To(BeFalse())
	})

	t.Run("returns an error for a nil node class", func(t *testing.T) {
		t.Parallel()

		g := NewWithT(t)
		err := imagefamily.ValidateImageFamilyCompatibility(nil, "1.32.0")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("AKSNodeClass is required"))
	})
}

func TestRequiresKubernetesVersionCompatibility(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		imageFamily *string
		want        bool
	}{
		"explicit ubuntu2204 is version pinned": {
			imageFamily: lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			want:        true,
		},
		"explicit ubuntu2404 is version pinned": {
			imageFamily: lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			want:        true,
		},
		"generic ubuntu is not version pinned": {
			imageFamily: lo.ToPtr(v1beta1.UbuntuImageFamily),
		},
		"unset image family is not version pinned": {},
		"azure linux is not version pinned": {
			imageFamily: lo.ToPtr(v1beta1.AzureLinuxImageFamily),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)
			nodeClass := &v1beta1.AKSNodeClass{
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: tc.imageFamily,
				},
			}

			g.Expect(imagefamily.RequiresKubernetesVersionCompatibility(nodeClass)).To(Equal(tc.want))
		})
	}

	t.Run("reports false for a nil node class", func(t *testing.T) {
		t.Parallel()

		g := NewWithT(t)
		g.Expect(imagefamily.RequiresKubernetesVersionCompatibility(nil)).To(BeFalse())
	})
}
