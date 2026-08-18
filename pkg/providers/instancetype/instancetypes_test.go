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

package instancetype

import (
	"testing"

	"github.com/Azure/skewer"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

// TestSupportsNestedVirtualization pins the family denylist used to decide which SKUs can run AKS
// Pod Sandboxing. resourceSkus exposes no nested-virtualization capability, so this mirrors the AKS
// RP's static Kata VM-size validation.
func TestSupportsNestedVirtualization(t *testing.T) {
	for _, tc := range []struct {
		size string
		want bool
	}{
		// Intel: nested virtualization from v3 onwards.
		{"D2_v2", false},
		{"DS2_v2", false},
		{"D2s_v3", true},
		{"D2_v3", true},
		{"D2ds_v4", true},
		{"D2s_v5", true},
		{"D2s_v6", true},
		{"E4s_v5", true},

		// AMD (the 'a' additive feature): only from v5 onwards.
		{"D2as_v4", false},
		{"D2a_v4", false},
		{"E4as_v4", false},
		{"D2as_v5", true},
		{"E4as_v5", true},
		{"D2as_v6", true},

		// A v1 family (no version suffix) has no nested virtualization.
		{"D2", false},
	} {
		t.Run(tc.size, func(t *testing.T) {
			g := NewWithT(t)
			sku := &skewer.SKU{Name: lo.ToPtr("Standard_" + tc.size), Size: lo.ToPtr(tc.size)}
			g.Expect(supportsNestedVirtualization(sku)).To(Equal(tc.want))
		})
	}
}

// An unparseable SKU name must not be filtered out — let the server reject it instead.
func TestSupportsNestedVirtualizationUnparseable(t *testing.T) {
	g := NewWithT(t)
	sku := &skewer.SKU{Name: lo.ToPtr("not-a-vm-size"), Size: lo.ToPtr("not-a-vm-size")}
	g.Expect(supportsNestedVirtualization(sku)).To(BeTrue(), "an unparseable SKU name should be allowed through")
}
