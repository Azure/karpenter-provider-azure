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
			name:        "Windows2019",
			imageFamily: v1beta1.Windows2019ImageFamily,
			expected:    "Windows2019",
		},
		{
			name:        "Windows2022",
			imageFamily: v1beta1.Windows2022ImageFamily,
			expected:    "Windows2022",
		},
		{
			name:        "Windows2025",
			imageFamily: v1beta1.Windows2025ImageFamily,
			expected:    "Windows2025",
		},
		{
			name:        "WindowsAnnual",
			imageFamily: v1beta1.WindowsAnnualImageFamily,
			expected:    "WindowsAnnual",
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

func TestGetOSForImageFamily(t *testing.T) {
	cases := []struct {
		name        string
		imageFamily string
		expected    string
	}{
		{name: "Ubuntu default", imageFamily: v1beta1.UbuntuImageFamily, expected: "linux"},
		{name: "Ubuntu2204", imageFamily: v1beta1.Ubuntu2204ImageFamily, expected: "linux"},
		{name: "Ubuntu2404", imageFamily: v1beta1.Ubuntu2404ImageFamily, expected: "linux"},
		{name: "AzureLinux", imageFamily: v1beta1.AzureLinuxImageFamily, expected: "linux"},
		{name: "empty string defaults to linux", imageFamily: "", expected: "linux"},
		{name: "unknown family defaults to linux", imageFamily: "CustomOS", expected: "linux"},
		{name: "Windows2019", imageFamily: v1beta1.Windows2019ImageFamily, expected: "windows"},
		{name: "Windows2022", imageFamily: v1beta1.Windows2022ImageFamily, expected: "windows"},
		{name: "Windows2025", imageFamily: v1beta1.Windows2025ImageFamily, expected: "windows"},
		{name: "WindowsAnnual", imageFamily: v1beta1.WindowsAnnualImageFamily, expected: "windows"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			result := v1beta1.GetOSForImageFamily(c.imageFamily)
			g.Expect(result).To(Equal(c.expected))
		})
	}
}
