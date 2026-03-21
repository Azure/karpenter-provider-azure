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

func TestIsAKSLabel(t *testing.T) {
	cases := []struct {
		name     string
		label    string
		expected bool
	}{
		{"AKS domain label", "kubernetes.azure.com/mode", true},
		{"AKS domain label - scalesetpriority", "kubernetes.azure.com/scalesetpriority", true},
		{"AKS domain label - arbitrary", "kubernetes.azure.com/anything", true},
		{"legacy label - agentpool", "agentpool", true},
		{"legacy label - storageprofile", "storageprofile", true},
		{"legacy label - storagetier", "storagetier", true},
		{"legacy label - accelerator", "accelerator", true},
		{"non-AKS label", "example.com/test", false},
		{"karpenter label", "karpenter.azure.com/sku-name", false},
		{"plain label", "my-label", false},
		{"kubernetes.io label", "kubernetes.io/os", false},
		{"prefix without slash", "kubernetes.azure.com", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(v1beta1.IsAKSLabel(c.label)).To(Equal(c.expected))
		})
	}
}

func TestIsAKSTaint(t *testing.T) {
	cases := []struct {
		name     string
		taintKey string
		expected bool
	}{
		{"AKS domain taint", "kubernetes.azure.com/scalesetpriority", true},
		{"AKS domain taint - mode", "kubernetes.azure.com/mode", true},
		{"AKS domain taint - arbitrary", "kubernetes.azure.com/anything", true},
		{"non-AKS taint", "example.com/test", false},
		{"plain taint key", "CriticalAddonsOnly", false},
		{"karpenter domain taint", "karpenter.azure.com/test", false},
		{"kubernetes.io taint", "node.kubernetes.io/not-ready", false},
		{"prefix without slash", "kubernetes.azure.com", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(v1beta1.IsAKSTaint(c.taintKey)).To(Equal(c.expected))
		})
	}
}
