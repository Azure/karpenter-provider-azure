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

package v1beta1_test

import (
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	. "github.com/onsi/gomega"
)

func TestGetOSSKUFromImageFamily(t *testing.T) {
	cases := []struct {
		name        string
		imageFamily string
		expected    string
	}{
		{
			name:        "Ubuntu default",
			imageFamily: v1beta1.UbuntuImageFamily,
			expected:    "Ubuntu",
		},
		{
			name:        "Ubuntu2204",
			imageFamily: v1beta1.Ubuntu2204ImageFamily,
			expected:    "Ubuntu",
		},
		{
			name:        "Ubuntu2404",
			imageFamily: v1beta1.Ubuntu2404ImageFamily,
			expected:    "Ubuntu",
		},
		{
			name:        "AzureLinux",
			imageFamily: v1beta1.AzureLinuxImageFamily,
			expected:    "AzureLinux",
		},
		{
			name:        "empty string defaults to Ubuntu",
			imageFamily: "",
			expected:    "Ubuntu",
		},
		{
			name:        "unknown family returns as-is",
			imageFamily: "CustomOS",
			expected:    "CustomOS",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result := v1beta1.GetOSSKUFromImageFamily(c.imageFamily)
			g.Expect(result).To(Equal(c.expected))
		})
	}
}
