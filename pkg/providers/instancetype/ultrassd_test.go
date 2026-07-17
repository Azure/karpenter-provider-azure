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

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	//nolint:staticcheck // deprecated package used by skewer
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"
	"github.com/Azure/skewer"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

func TestUltraSSDOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sku  *skewer.SKU
		zone string
		want []string
	}{
		{
			name: "regional capability enables a regional offering",
			sku:  ultraSSDSKU(true, nil),
			zone: zones.Regional,
			want: []string{"true", "false"},
		},
		{
			name: "zonal capability does not enable a regional offering",
			sku:  ultraSSDSKU(false, map[string]bool{"1": true}),
			zone: zones.Regional,
			want: []string{"false"},
		},
		{
			name: "zonal capability enables its zonal offering",
			sku:  ultraSSDSKU(false, map[string]bool{"1": true}),
			zone: "westus3-1",
			want: []string{"true", "false"},
		},
		{
			name: "regional capability does not enable a zonal offering",
			sku:  ultraSSDSKU(true, nil),
			zone: "westus3-1",
			want: []string{"false"},
		},
		{
			name: "zonal capability does not enable another zone",
			sku:  ultraSSDSKU(false, map[string]bool{"2": true}),
			zone: "westus3-1",
			want: []string{"false"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(ultraSSDOptions(test.sku, test.zone)).To(Equal(test.want))
		})
	}
}

func ultraSSDSKU(regional bool, zonal map[string]bool) *skewer.SKU {
	capabilities := []compute.ResourceSkuCapabilities{{
		Name:  new(skewer.UltraSSDAvailable),
		Value: new(lo.Ternary(regional, "True", "False")),
	}}
	zoneDetails := lo.MapToSlice(zonal, func(zone string, supported bool) compute.ResourceSkuZoneDetails {
		names := []string{zone}
		zoneCapabilities := []compute.ResourceSkuCapabilities{{
			Name:  new(skewer.UltraSSDAvailable),
			Value: new(lo.Ternary(supported, "True", "False")),
		}}
		return compute.ResourceSkuZoneDetails{
			Name:         &names,
			Capabilities: &zoneCapabilities,
		}
	})
	locationInfo := []compute.ResourceSkuLocationInfo{{ZoneDetails: &zoneDetails}}
	sku := skewer.SKU(compute.ResourceSku{
		Capabilities: &capabilities,
		LocationInfo: &locationInfo,
	})
	return &sku
}
