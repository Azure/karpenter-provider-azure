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

	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

func TestResolvesToUbuntu2004(t *testing.T) {
	cases := []struct {
		name   string
		family *string
		fips   *v1beta1.FIPSMode
		want   bool
	}{
		{"nil family + nil fips", nil, nil, false},
		{"nil family + FIPS", nil, lo.ToPtr(v1beta1.FIPSModeFIPS), true},
		{"nil family + FIPS Disabled", nil, lo.ToPtr(v1beta1.FIPSModeDisabled), false},
		{"empty family + FIPS", lo.ToPtr(""), lo.ToPtr(v1beta1.FIPSModeFIPS), true},
		{"Ubuntu legacy + FIPS", lo.ToPtr(v1beta1.UbuntuImageFamily), lo.ToPtr(v1beta1.FIPSModeFIPS), true},
		{"Ubuntu legacy + nil fips", lo.ToPtr(v1beta1.UbuntuImageFamily), nil, false},
		{"Ubuntu legacy + Disabled", lo.ToPtr(v1beta1.UbuntuImageFamily), lo.ToPtr(v1beta1.FIPSModeDisabled), false},
		{"Ubuntu2204 + FIPS", lo.ToPtr(v1beta1.Ubuntu2204ImageFamily), lo.ToPtr(v1beta1.FIPSModeFIPS), false},
		{"Ubuntu2404 + FIPS", lo.ToPtr(v1beta1.Ubuntu2404ImageFamily), lo.ToPtr(v1beta1.FIPSModeFIPS), false},
		{"AzureLinux + FIPS", lo.ToPtr(v1beta1.AzureLinuxImageFamily), lo.ToPtr(v1beta1.FIPSModeFIPS), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imagefamily.ResolvesToUbuntu2004(tc.family, tc.fips)
			if got != tc.want {
				t.Fatalf("ResolvesToUbuntu2004(%v, %v) = %v, want %v", lo.FromPtr(tc.family), lo.FromPtr(tc.fips), got, tc.want)
			}
		})
	}
}
