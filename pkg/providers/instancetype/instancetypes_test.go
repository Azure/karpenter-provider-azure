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

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute" //nolint:staticcheck
	"github.com/Azure/skewer"
)

func TestInstanceTypeZones(t *testing.T) {
	p := &DefaultProvider{region: "westus2"}

	t.Run("zone-restricted SKU returns empty set", func(t *testing.T) {
		// SKU supports zones 1/2/3 but subscription is blocked from all of them via
		// NotAvailableForSubscription. Before the fix, this returned sets.New(""),
		// injecting "" as a valid topology domain and breaking TopologySpreadConstraints.
		zones := []string{"1", "2", "3"}
		sku := (*skewer.SKU)(&compute.ResourceSku{
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{
				Location: toStringPtr("westus2"),
				Zones:    &zones,
			}},
			Restrictions: &[]compute.ResourceSkuRestrictions{{
				Type:       compute.Zone,
				ReasonCode: compute.NotAvailableForSubscription,
				Values:     &[]string{"westus2"},
				RestrictionInfo: &compute.ResourceSkuRestrictionInfo{
					Zones: &zones,
				},
			}},
		})
		result := p.instanceTypeZones(sku)
		if result.Len() != 0 {
			t.Errorf("expected empty zone set for zone-restricted SKU, got %v", result)
		}
	})

	t.Run("truly non-zonal SKU returns set with empty string", func(t *testing.T) {
		// SKU has no zones defined in this region at all — preserve existing behavior
		// so non-zonal regions like westcentralus continue to work.
		zones := []string{}
		sku := (*skewer.SKU)(&compute.ResourceSku{
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{
				Location: toStringPtr("westus2"),
				Zones:    &zones,
			}},
		})
		result := p.instanceTypeZones(sku)
		if !result.Has("") {
			t.Errorf(`expected zone="" for truly non-zonal SKU, got %v`, result)
		}
	})

	t.Run("zonal SKU returns correct AKS zone labels", func(t *testing.T) {
		zones := []string{"1", "2", "3"}
		sku := (*skewer.SKU)(&compute.ResourceSku{
			LocationInfo: &[]compute.ResourceSkuLocationInfo{{
				Location: toStringPtr("westus2"),
				Zones:    &zones,
			}},
		})
		result := p.instanceTypeZones(sku)
		for _, expected := range []string{"westus2-1", "westus2-2", "westus2-3"} {
			if !result.Has(expected) {
				t.Errorf("expected zone %q in result, got %v", expected, result)
			}
		}
	})
}

func toStringPtr(s string) *string { return &s }
