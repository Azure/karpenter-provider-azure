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

package imagefamily

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

func TestIsNewerVersion(t *testing.T) {
	testCases := []struct {
		version1 string
		version2 string
		expected bool
	}{
		{"202308.28.0", "202411.12.0", false},
		{"202411.12.0", "202308.28.0", true},
		{"202202.08.29", "202405.20.0", false},
		{"202404.09.0", "202411.12.0", false},
		{"202405.20.0", "202404.09.0", true},
		{"2022.10.03", "2022.12.15", false},
		{"202411.12.0", "2022.12.15", true},
		{"2022.12.15", "2022.10.03", true},
		{"202411.12.0", "202411.12.0", false},
		{"2o2411.12.0", "202411.12.0", false}, // invalid version strings should be ignored and return false

		// Security-patch versions use the "<baseVersion>-<securityPatchDate>" scheme. The security
		// patch date is the primary ordering key; the base version only breaks ties.
		{"202606.08.1-2026.06.13", "202604.24.0-2026.05.24", true},  // newer base + newer patch date
		{"202604.24.0-2026.05.24", "202606.08.1-2026.06.13", false}, // older
		{"202605.05.1-2026.06.13", "202605.05.1-2026.05.24", true},  // same base, newer patch date
		{"202605.05.1-2026.05.24", "202605.05.1-2026.06.13", false}, // same base, older patch date
		{"202605.14.0-2026.06.13", "202605.05.1-2026.06.13", true},  // same patch date, newer base
		{"202605.05.1-2026.06.13", "202605.14.0-2026.06.13", false}, // same patch date, older base
		{"202606.08.1-2026.06.13", "202606.08.1-2026.06.13", false}, // equal
		// A newer security patch date on an OLDER base image must win: it carries newer security
		// fixes. This is the case a base-version-first comparison gets wrong.
		{"202602.19.0-2026.03.02", "202602.20.0-2026.03.01", true},
		{"202602.20.0-2026.03.01", "202602.19.0-2026.03.02", false},
	}

	for _, tc := range testCases {
		t.Run(tc.version1+"_"+tc.version2, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isNewerVersion(tc.version1, tc.version2)).To(Equal(tc.expected))
		})
	}
}

// TestFilteredNodeImagesSecurityPatch verifies that, given multiple security-patch versions for the
// same OS/SKU (as returned by the SecurityPatchOnly nodeImageVersions response), FilteredNodeImages
// selects the newest one. Ordering is intentionally not latest-first in the input to guard against a
// regression where the first-seen image is kept.
func TestFilteredNodeImagesSecurityPatch(t *testing.T) {
	g := NewWithT(t)
	img := func(sku, version string) *armcontainerservice.NodeImageVersion {
		return &armcontainerservice.NodeImageVersion{
			OS:      lo.ToPtr(AKSUbuntuGalleryName),
			SKU:     lo.ToPtr(sku),
			Version: lo.ToPtr(version),
		}
	}
	input := []*armcontainerservice.NodeImageVersion{
		img("2404gen2containerd", "202604.24.0-2026.05.24"), // oldest patch date first on purpose
		img("2404gen2containerd", "202605.05.1-2026.06.13"),
		img("2404gen2containerd", "202606.08.1-2026.06.13"), // newest
		img("2404gen2containerd", "202605.14.0-2026.05.24"),
	}

	filtered := FilteredNodeImages(input)
	g.Expect(filtered).To(HaveLen(1))
	g.Expect(lo.FromPtr(filtered[0].Version)).To(Equal("202606.08.1-2026.06.13"))
}

// TestFilteredNodeImagesSecurityPatchPrefersNewerPatchDate verifies that a newer security patch date
// wins even when its base image version is older, matching how the service orders security VHDs.
func TestFilteredNodeImagesSecurityPatchPrefersNewerPatchDate(t *testing.T) {
	g := NewWithT(t)
	img := func(version string) *armcontainerservice.NodeImageVersion {
		return &armcontainerservice.NodeImageVersion{
			OS:      lo.ToPtr(AKSUbuntuGalleryName),
			SKU:     lo.ToPtr("2404gen2containerd"),
			Version: lo.ToPtr(version),
		}
	}
	input := []*armcontainerservice.NodeImageVersion{
		img("202602.20.0-2026.03.01"), // newer base, older security patch date
		img("202602.19.0-2026.03.02"), // older base, newer security patch date -> must win
	}

	filtered := FilteredNodeImages(input)
	g.Expect(filtered).To(HaveLen(1))
	g.Expect(lo.FromPtr(filtered[0].Version)).To(Equal("202602.19.0-2026.03.02"))
}
