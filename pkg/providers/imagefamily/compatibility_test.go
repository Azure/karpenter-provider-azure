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
		"ubuntu2204 rejects versions below lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.25.1",
			wantErr:           []string{"effective image family", v1beta1.Ubuntu2204ImageFamily, "1.25.1", "supported range is >= 1.25.2 and < 1.37.0"},
		},
		"ubuntu2204 accepts lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.25.2",
		},
		"ubuntu2204 accepts below upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.36.9",
		},
		"ubuntu2204 rejects upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "1.37.0",
			wantErr:           []string{"effective image family", v1beta1.Ubuntu2204ImageFamily, "1.37.0", "supported range is >= 1.25.2 and < 1.37.0"},
		},
		"ubuntu2204 with fips accepts below extended upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			trustedLaunch:     true,
			kubernetesVersion: "1.38.9",
		},
		"ubuntu2204 with fips rejects extended upper bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			fipsMode:          lo.ToPtr(v1beta1.FIPSModeFIPS),
			trustedLaunch:     true,
			kubernetesVersion: "1.39.0",
			wantErr:           []string{"effective image family", v1beta1.Ubuntu2204ImageFamily, "1.39.0", "with FIPS", "supported range is >= 1.25.2 and < 1.39.0"},
		},
		"ubuntu2404 rejects versions below lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.31.9",
			wantErr:           []string{"effective image family", v1beta1.Ubuntu2404ImageFamily, "1.31.9", "supported range is >= 1.32.0"},
		},
		"ubuntu2404 accepts lower bound": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.32.0",
		},
		"generic ubuntu resolves to ubuntu2404 compatibility for later clusters": {
			imageFamily:       lo.ToPtr(v1beta1.UbuntuImageFamily),
			kubernetesVersion: "1.39.0",
		},
		"unset ubuntu resolves to ubuntu2204 compatibility for earlier clusters": {
			kubernetesVersion: "1.25.1",
			wantErr:           []string{"effective image family", v1beta1.Ubuntu2204ImageFamily, "1.25.1", "supported range is >= 1.25.2 and < 1.37.0"},
		},
		"azlinux remains unrestricted": {
			imageFamily:       lo.ToPtr(v1beta1.AzureLinuxImageFamily),
			kubernetesVersion: "1.20.0",
		},
		"rejects malformed kubernetes versions": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2204ImageFamily),
			kubernetesVersion: "not-a-version",
			wantErr:           []string{"malformed discovered Kubernetes version", "not-a-version", "semantic version"},
		},
		"accepts tolerant two segment versions": {
			imageFamily:       lo.ToPtr(v1beta1.Ubuntu2404ImageFamily),
			kubernetesVersion: "1.32",
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
		})
	}
}
