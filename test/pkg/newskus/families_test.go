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

package newskus

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
)

func TestCanonicalFamily(t *testing.T) {
	tests := []struct {
		name     string
		entry    instancetype.SKUEntry
		expected string
	}{
		{"AMD D-series v7", instancetype.SKUEntry{Name: "Standard_D2as_v7"}, "Da_v7"},
		{"AMD D-series v7 with disk", instancetype.SKUEntry{Name: "Standard_D2ads_v7"}, "Da_v7"},
		{"AMD D-series v7 with local+disk", instancetype.SKUEntry{Name: "Standard_D4alds_v7"}, "Da_v7"},
		{"AMD D-series v7 plain", instancetype.SKUEntry{Name: "Standard_D16als_v7"}, "Da_v7"},
		{"ARM D-series v6", instancetype.SKUEntry{Name: "Standard_D2ps_v6"}, "Dp_v6"},
		{"ARM D-series v6 with disk", instancetype.SKUEntry{Name: "Standard_D4pds_v6"}, "Dp_v6"},
		{"ARM D-series v6 with local+disk", instancetype.SKUEntry{Name: "Standard_D8plds_v6"}, "Dp_v6"},
		{"Intel D-series v5", instancetype.SKUEntry{Name: "Standard_D2s_v5"}, "D_v5"},
		{"Intel D-series v5 with disk", instancetype.SKUEntry{Name: "Standard_D4ds_v5"}, "D_v5"},
		{"Intel D-series v5 with local+disk", instancetype.SKUEntry{Name: "Standard_D8lds_v5"}, "D_v5"},
		{"E-series memory AMD", instancetype.SKUEntry{Name: "Standard_E2as_v5"}, "Ea_v5"},
		{"E-series memory Intel", instancetype.SKUEntry{Name: "Standard_E4s_v5"}, "E_v5"},
		{"E-series memory ARM", instancetype.SKUEntry{Name: "Standard_E2ps_v5"}, "Ep_v5"},
		{"B-series burstable", instancetype.SKUEntry{Name: "Standard_B2s_v2"}, "B_v2"},
		{"B-series burstable AMD", instancetype.SKUEntry{Name: "Standard_B2as_v2"}, "Ba_v2"},
		{"B-series burstable ARM", instancetype.SKUEntry{Name: "Standard_B2ps_v2"}, "Bp_v2"},
		{"M-series memory-intensive", instancetype.SKUEntry{Name: "Standard_M8ms"}, "Mm"},
		{"F-series compute", instancetype.SKUEntry{Name: "Standard_F2s_v2"}, "F_v2"},
		{"L-series storage", instancetype.SKUEntry{Name: "Standard_L8s_v3"}, "L_v3"},
		{"No Standard_ prefix", instancetype.SKUEntry{Name: "D2s_v5"}, "D_v5"},
		{"o-additive-feature", instancetype.SKUEntry{Name: "Standard_L4aos_v4"}, "La_v4"},
		{"Unparsable falls back to Family (unknown z)", instancetype.SKUEntry{Name: "Standard_L4azs_v4", Family: "standardLazsv4Family"}, "standardLazsv4Family"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(CanonicalFamily(tt.entry)).To(Equal(tt.expected))
		})
	}
}

func TestVcpuCount(t *testing.T) {
	tests := []struct {
		name     string
		expected int
	}{
		{"Standard_D2as_v7", 2},
		{"Standard_D4as_v7", 4},
		{"Standard_D16ads_v7", 16},
		{"Standard_D96as_v7", 96},
		{"Standard_E128s_v6", 128},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(VcpuCount(tt.name)).To(Equal(tt.expected))
		})
	}
}

func TestPickRepresentativeSize(t *testing.T) {
	tests := []struct {
		name     string
		skus     []instancetype.SKUEntry
		expected string
	}{
		{
			name: "picks simplest variant with >=4 vCPUs",
			skus: []instancetype.SKUEntry{
				{Name: "Standard_D2as_v7"},
				{Name: "Standard_D4as_v7"},
				{Name: "Standard_D8as_v7"},
				{Name: "Standard_D2ads_v7"},
				{Name: "Standard_D4ads_v7"},
				{Name: "Standard_D2alds_v7"},
				{Name: "Standard_D4alds_v7"},
			},
			expected: "Standard_D4as_v7",
		},
		{
			name: "alphabetical tiebreaker among same features and vCPUs",
			skus: []instancetype.SKUEntry{
				{Name: "Standard_D4bs_v7"},
				{Name: "Standard_D4as_v7"},
			},
			expected: "Standard_D4as_v7",
		},
		{
			name: "falls back to smaller if no >=4",
			skus: []instancetype.SKUEntry{
				{Name: "Standard_D2as_v7"},
			},
			expected: "Standard_D2as_v7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(PickRepresentativeSize(tt.skus).Name).To(Equal(tt.expected))
		})
	}
}
