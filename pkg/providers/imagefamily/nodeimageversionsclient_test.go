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
	g := NewWithT(t)
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
	}

	for _, tc := range testCases {
		t.Run(tc.version1+"_"+tc.version2, func(t *testing.T) {
			result := isNewerVersion(tc.version1, tc.version2)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestFilteredNodeImagesPrefersSecurityPatch(t *testing.T) {
	g := NewWithT(t)
	images := []*armcontainerservice.NodeImageVersion{
		{OS: lo.ToPtr("AKSUbuntu"), SKU: lo.ToPtr("2204gen2containerd"), Version: lo.ToPtr("202609.23.0")},
		{OS: lo.ToPtr("AKSUbuntu"), SKU: lo.ToPtr("2204gen2containerd"), Version: lo.ToPtr("202608.14.0-2026.09.25")},
		{OS: lo.ToPtr("AKSUbuntu"), SKU: lo.ToPtr("2204gen2containerd"), Version: lo.ToPtr("202608.01.0-2026.09.26")},
		{OS: lo.ToPtr("AKSUbuntu"), SKU: lo.ToPtr("kata"), Version: lo.ToPtr("202609.23.0")},
	}

	filtered := FilteredNodeImages(images)
	g.Expect(filtered).To(HaveLen(2))
	g.Expect(lo.Map(filtered, func(image *armcontainerservice.NodeImageVersion, _ int) string { return lo.FromPtr(image.Version) })).To(ConsistOf(
		"202608.14.0-2026.09.25", "202609.23.0",
	))
}
