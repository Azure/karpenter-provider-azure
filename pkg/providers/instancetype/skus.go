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
	"github.com/Azure/skewer"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
)

// GetKarpenterWorkingSKUs returns a the list of SKUs that are
// allowed to be used by Karpenter. This is a subset of the
// SKUs that are available in Azure.
func GetKarpenterWorkingSKUs() []skewer.SKU {
	workingSKUs := []skewer.SKU{}
	for _, sku := range allAzureVMSkus {
		var exclude bool
		// If we find this SKU in the AKS restricted list, exclude it
		for _, aksRestrictedSKU := range AKSRestrictedVMSizes.UnsortedList() {
			if aksRestrictedSKU == sku.GetName() {
				exclude = true
			}
		}
		// If it's not in the AKS restricted list, it may be in the Karpenter restricted list
		if !exclude {
			for _, karpenterRestrictedSKU := range karpenterRestrictedVMSKUs.UnsortedList() {
				if karpenterRestrictedSKU == sku.GetName() {
					exclude = true
				}
			}
		}
		// If it's not in any of the restricted lists, we register it as a working VM SKU
		if !exclude {
			workingSKUs = append(workingSKUs, sku)
		}
	}
	return workingSKUs
}

var (
	// AKSRestrictedVMSizes are low-performance VM sizes
	// that are not allowed for use in AKS node pools.
	AKSRestrictedVMSizes = sets.New(
		"Standard_A0",
		"Standard_A1",
		"Standard_A1_v2",
		"Standard_B1s",
		"Standard_B1ms",
		"Standard_F1",
		"Standard_F1s",
		"Basic_A0",
		"Basic_A1",
		"Basic_A2",
		"Basic_A3",
		"Basic_A4",
	)
	// karpenterRestrictedVMSKUs are VMS SKUs that are known to
	// be problematic with karpenter-provider-azure.
	karpenterRestrictedVMSKUs = sets.New[string]()
)

// allAzureVMSkus is a generated list from https://github.com/Azure/skewer
// git clone https://github.com/Azure/skewer.git
// $ cd skewer/hack
// $ go run ./generate_vmsize_testdata.go
// $ cat ../testdata/generated_vmskus_testdata.go
var allAzureVMSkus = []skewer.SKU{
	{
		Name: ptr.To("Basic_A0"),
	},
	{
		Name: ptr.To("Basic_A1"),
	},
	{
		Name: ptr.To("Basic_A2"),
	},
	{
		Name: ptr.To("Basic_A3"),
	},
	{
		Name: ptr.To("Basic_A4"),
	},
	{
		Name: ptr.To("Standard_A0"),
	},
	{
		Name: ptr.To("Standard_A1"),
	},
	{
		Name: ptr.To("Standard_A1_v2"),
	},
	{
		Name: ptr.To("Standard_A2"),
	},
	{
		Name: ptr.To("Standard_A2m_v2"),
	},
	{
		Name: ptr.To("Standard_A2_v2"),
	},
	{
		Name: ptr.To("Standard_A3"),
	},
	{
		Name: ptr.To("Standard_A4"),
	},
	{
		Name: ptr.To("Standard_A4m_v2"),
	},
	{
		Name: ptr.To("Standard_A4_v2"),
	},
	{
		Name: ptr.To("Standard_A5"),
	},
	{
		Name: ptr.To("Standard_A6"),
	},
	{
		Name: ptr.To("Standard_A7"),
	},
	{
		Name: ptr.To("Standard_A8m_v2"),
	},
	{
		Name: ptr.To("Standard_A8_v2"),
	},
	{
		Name: ptr.To("Standard_B12ms"),
	},
	{
		Name: ptr.To("Standard_B16als_v2"),
	},
	{
		Name: ptr.To("Standard_B16as_v2"),
	},
	{
		Name: ptr.To("Standard_B16ls_v2"),
	},
	{
		Name: ptr.To("Standard_B16ms"),
	},
	{
		Name: ptr.To("Standard_B16pls_v2"),
	},
	{
		Name: ptr.To("Standard_B16ps_v2"),
	},
	{
		Name: ptr.To("Standard_B16s_v2"),
	},
	{
		Name: ptr.To("Standard_B1ls"),
	},
	{
		Name: ptr.To("Standard_B1ms"),
	},
	{
		Name: ptr.To("Standard_B1s"),
	},
	{
		Name: ptr.To("Standard_B20ms"),
	},
	{
		Name: ptr.To("Standard_B2als_v2"),
	},
	{
		Name: ptr.To("Standard_B2as_v2"),
	},
	{
		Name: ptr.To("Standard_B2ats_v2"),
	},
	{
		Name: ptr.To("Standard_B2ls_v2"),
	},
	{
		Name: ptr.To("Standard_B2ms"),
	},
	{
		Name: ptr.To("Standard_B2pls_v2"),
	},
	{
		Name: ptr.To("Standard_B2ps_v2"),
	},
	{
		Name: ptr.To("Standard_B2pts_v2"),
	},
	{
		Name: ptr.To("Standard_B2s"),
	},
	{
		Name: ptr.To("Standard_B2s_v2"),
	},
	{
		Name: ptr.To("Standard_B2ts_v2"),
	},
	{
		Name: ptr.To("Standard_B32als_v2"),
	},
	{
		Name: ptr.To("Standard_B32as_v2"),
	},
	{
		Name: ptr.To("Standard_B32ls_v2"),
	},
	{
		Name: ptr.To("Standard_B32s_v2"),
	},
	{
		Name: ptr.To("Standard_B4als_v2"),
	},
	{
		Name: ptr.To("Standard_B4as_v2"),
	},
	{
		Name: ptr.To("Standard_B4ls_v2"),
	},
	{
		Name: ptr.To("Standard_B4ms"),
	},
	{
		Name: ptr.To("Standard_B4pls_v2"),
	},
	{
		Name: ptr.To("Standard_B4ps_v2"),
	},
	{
		Name: ptr.To("Standard_B4s_v2"),
	},
	{
		Name: ptr.To("Standard_B8als_v2"),
	},
	{
		Name: ptr.To("Standard_B8as_v2"),
	},
	{
		Name: ptr.To("Standard_B8ls_v2"),
	},
	{
		Name: ptr.To("Standard_B8ms"),
	},
	{
		Name: ptr.To("Standard_B8pls_v2"),
	},
	{
		Name: ptr.To("Standard_B8ps_v2"),
	},
	{
		Name: ptr.To("Standard_B8s_v2"),
	},
	{
		Name: ptr.To("Standard_D1"),
	},
	{
		Name: ptr.To("Standard_D11"),
	},
	{
		Name: ptr.To("Standard_D11_v2"),
	},
	{
		Name: ptr.To("Standard_D11_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D12"),
	},
	{
		Name: ptr.To("Standard_D128ds_v6"),
	},
	{
		Name: ptr.To("Standard_D128lds_v6"),
	},
	{
		Name: ptr.To("Standard_D128ls_v6"),
	},
	{
		Name: ptr.To("Standard_D128s_v6"),
	},
	{
		Name: ptr.To("Standard_D12_v2"),
	},
	{
		Name: ptr.To("Standard_D12_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D13"),
	},
	{
		Name: ptr.To("Standard_D13_v2"),
	},
	{
		Name: ptr.To("Standard_D13_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D14"),
	},
	{
		Name: ptr.To("Standard_D14_v2"),
	},
	{
		Name: ptr.To("Standard_D14_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D15_v2"),
	},
	{
		Name: ptr.To("Standard_D16ads_v5"),
	},
	{
		Name: ptr.To("Standard_D16ads_v6"),
	},
	{
		Name: ptr.To("Standard_D16alds_v6"),
	},
	{
		Name: ptr.To("Standard_D16als_v6"),
	},
	{
		Name: ptr.To("Standard_D16as_v4"),
	},
	{
		Name: ptr.To("Standard_D16as_v5"),
	},
	{
		Name: ptr.To("Standard_D16as_v6"),
	},
	{
		Name: ptr.To("Standard_D16a_v4"),
	},
	{
		Name: ptr.To("Standard_D16ds_v4"),
	},
	{
		Name: ptr.To("Standard_D16ds_v5"),
	},
	{
		Name: ptr.To("Standard_D16ds_v6"),
	},
	{
		Name: ptr.To("Standard_D16d_v4"),
	},
	{
		Name: ptr.To("Standard_D16d_v5"),
	},
	{
		Name: ptr.To("Standard_D16lds_v5"),
	},
	{
		Name: ptr.To("Standard_D16lds_v6"),
	},
	{
		Name: ptr.To("Standard_D16ls_v5"),
	},
	{
		Name: ptr.To("Standard_D16ls_v6"),
	},
	{
		Name: ptr.To("Standard_D16pds_v5"),
	},
	{
		Name: ptr.To("Standard_D16pds_v6"),
	},
	{
		Name: ptr.To("Standard_D16plds_v5"),
	},
	{
		Name: ptr.To("Standard_D16plds_v6"),
	},
	{
		Name: ptr.To("Standard_D16pls_v5"),
	},
	{
		Name: ptr.To("Standard_D16pls_v6"),
	},
	{
		Name: ptr.To("Standard_D16ps_v5"),
	},
	{
		Name: ptr.To("Standard_D16ps_v6"),
	},
	{
		Name: ptr.To("Standard_D16s_v3"),
	},
	{
		Name: ptr.To("Standard_D16s_v4"),
	},
	{
		Name: ptr.To("Standard_D16s_v5"),
	},
	{
		Name: ptr.To("Standard_D16s_v6"),
	},
	{
		Name: ptr.To("Standard_D16_v3"),
	},
	{
		Name: ptr.To("Standard_D16_v4"),
	},
	{
		Name: ptr.To("Standard_D16_v5"),
	},
	{
		Name: ptr.To("Standard_D192ds_v6"),
	},
	{
		Name: ptr.To("Standard_D192s_v6"),
	},
	{
		Name: ptr.To("Standard_D1_v2"),
	},
	{
		Name: ptr.To("Standard_D2"),
	},
	{
		Name: ptr.To("Standard_D2ads_v5"),
	},
	{
		Name: ptr.To("Standard_D2ads_v6"),
	},
	{
		Name: ptr.To("Standard_D2alds_v6"),
	},
	{
		Name: ptr.To("Standard_D2als_v6"),
	},
	{
		Name: ptr.To("Standard_D2as_v4"),
	},
	{
		Name: ptr.To("Standard_D2as_v5"),
	},
	{
		Name: ptr.To("Standard_D2as_v6"),
	},
	{
		Name: ptr.To("Standard_D2a_v4"),
	},
	{
		Name: ptr.To("Standard_D2ds_v4"),
	},
	{
		Name: ptr.To("Standard_D2ds_v5"),
	},
	{
		Name: ptr.To("Standard_D2ds_v6"),
	},
	{
		Name: ptr.To("Standard_D2d_v4"),
	},
	{
		Name: ptr.To("Standard_D2d_v5"),
	},
	{
		Name: ptr.To("Standard_D2lds_v5"),
	},
	{
		Name: ptr.To("Standard_D2lds_v6"),
	},
	{
		Name: ptr.To("Standard_D2ls_v5"),
	},
	{
		Name: ptr.To("Standard_D2ls_v6"),
	},
	{
		Name: ptr.To("Standard_D2pds_v5"),
	},
	{
		Name: ptr.To("Standard_D2pds_v6"),
	},
	{
		Name: ptr.To("Standard_D2plds_v5"),
	},
	{
		Name: ptr.To("Standard_D2plds_v6"),
	},
	{
		Name: ptr.To("Standard_D2pls_v5"),
	},
	{
		Name: ptr.To("Standard_D2pls_v6"),
	},
	{
		Name: ptr.To("Standard_D2ps_v5"),
	},
	{
		Name: ptr.To("Standard_D2ps_v6"),
	},
	{
		Name: ptr.To("Standard_D2s_v3"),
	},
	{
		Name: ptr.To("Standard_D2s_v4"),
	},
	{
		Name: ptr.To("Standard_D2s_v5"),
	},
	{
		Name: ptr.To("Standard_D2s_v6"),
	},
	{
		Name: ptr.To("Standard_D2_v2"),
	},
	{
		Name: ptr.To("Standard_D2_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D2_v3"),
	},
	{
		Name: ptr.To("Standard_D2_v4"),
	},
	{
		Name: ptr.To("Standard_D2_v5"),
	},
	{
		Name: ptr.To("Standard_D3"),
	},
	{
		Name: ptr.To("Standard_D32ads_v5"),
	},
	{
		Name: ptr.To("Standard_D32ads_v6"),
	},
	{
		Name: ptr.To("Standard_D32alds_v6"),
	},
	{
		Name: ptr.To("Standard_D32als_v6"),
	},
	{
		Name: ptr.To("Standard_D32as_v4"),
	},
	{
		Name: ptr.To("Standard_D32as_v5"),
	},
	{
		Name: ptr.To("Standard_D32as_v6"),
	},
	{
		Name: ptr.To("Standard_D32a_v4"),
	},
	{
		Name: ptr.To("Standard_D32ds_v4"),
	},
	{
		Name: ptr.To("Standard_D32ds_v5"),
	},
	{
		Name: ptr.To("Standard_D32ds_v6"),
	},
	{
		Name: ptr.To("Standard_D32d_v4"),
	},
	{
		Name: ptr.To("Standard_D32d_v5"),
	},
	{
		Name: ptr.To("Standard_D32lds_v5"),
	},
	{
		Name: ptr.To("Standard_D32lds_v6"),
	},
	{
		Name: ptr.To("Standard_D32ls_v5"),
	},
	{
		Name: ptr.To("Standard_D32ls_v6"),
	},
	{
		Name: ptr.To("Standard_D32pds_v5"),
	},
	{
		Name: ptr.To("Standard_D32pds_v6"),
	},
	{
		Name: ptr.To("Standard_D32plds_v5"),
	},
	{
		Name: ptr.To("Standard_D32plds_v6"),
	},
	{
		Name: ptr.To("Standard_D32pls_v5"),
	},
	{
		Name: ptr.To("Standard_D32pls_v6"),
	},
	{
		Name: ptr.To("Standard_D32ps_v5"),
	},
	{
		Name: ptr.To("Standard_D32ps_v6"),
	},
	{
		Name: ptr.To("Standard_D32s_v3"),
	},
	{
		Name: ptr.To("Standard_D32s_v4"),
	},
	{
		Name: ptr.To("Standard_D32s_v5"),
	},
	{
		Name: ptr.To("Standard_D32s_v6"),
	},
	{
		Name: ptr.To("Standard_D32_v3"),
	},
	{
		Name: ptr.To("Standard_D32_v4"),
	},
	{
		Name: ptr.To("Standard_D32_v5"),
	},
	{
		Name: ptr.To("Standard_D3_v2"),
	},
	{
		Name: ptr.To("Standard_D3_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D4"),
	},
	{
		Name: ptr.To("Standard_D48ads_v5"),
	},
	{
		Name: ptr.To("Standard_D48ads_v6"),
	},
	{
		Name: ptr.To("Standard_D48alds_v6"),
	},
	{
		Name: ptr.To("Standard_D48als_v6"),
	},
	{
		Name: ptr.To("Standard_D48as_v4"),
	},
	{
		Name: ptr.To("Standard_D48as_v5"),
	},
	{
		Name: ptr.To("Standard_D48as_v6"),
	},
	{
		Name: ptr.To("Standard_D48a_v4"),
	},
	{
		Name: ptr.To("Standard_D48ds_v4"),
	},
	{
		Name: ptr.To("Standard_D48ds_v5"),
	},
	{
		Name: ptr.To("Standard_D48ds_v6"),
	},
	{
		Name: ptr.To("Standard_D48d_v4"),
	},
	{
		Name: ptr.To("Standard_D48d_v5"),
	},
	{
		Name: ptr.To("Standard_D48lds_v5"),
	},
	{
		Name: ptr.To("Standard_D48lds_v6"),
	},
	{
		Name: ptr.To("Standard_D48ls_v5"),
	},
	{
		Name: ptr.To("Standard_D48ls_v6"),
	},
	{
		Name: ptr.To("Standard_D48pds_v5"),
	},
	{
		Name: ptr.To("Standard_D48pds_v6"),
	},
	{
		Name: ptr.To("Standard_D48plds_v5"),
	},
	{
		Name: ptr.To("Standard_D48plds_v6"),
	},
	{
		Name: ptr.To("Standard_D48pls_v5"),
	},
	{
		Name: ptr.To("Standard_D48pls_v6"),
	},
	{
		Name: ptr.To("Standard_D48ps_v5"),
	},
	{
		Name: ptr.To("Standard_D48ps_v6"),
	},
	{
		Name: ptr.To("Standard_D48s_v3"),
	},
	{
		Name: ptr.To("Standard_D48s_v4"),
	},
	{
		Name: ptr.To("Standard_D48s_v5"),
	},
	{
		Name: ptr.To("Standard_D48s_v6"),
	},
	{
		Name: ptr.To("Standard_D48_v3"),
	},
	{
		Name: ptr.To("Standard_D48_v4"),
	},
	{
		Name: ptr.To("Standard_D48_v5"),
	},
	{
		Name: ptr.To("Standard_D4ads_v5"),
	},
	{
		Name: ptr.To("Standard_D4ads_v6"),
	},
	{
		Name: ptr.To("Standard_D4alds_v6"),
	},
	{
		Name: ptr.To("Standard_D4als_v6"),
	},
	{
		Name: ptr.To("Standard_D4as_v4"),
	},
	{
		Name: ptr.To("Standard_D4as_v5"),
	},
	{
		Name: ptr.To("Standard_D4as_v6"),
	},
	{
		Name: ptr.To("Standard_D4a_v4"),
	},
	{
		Name: ptr.To("Standard_D4ds_v4"),
	},
	{
		Name: ptr.To("Standard_D4ds_v5"),
	},
	{
		Name: ptr.To("Standard_D4ds_v6"),
	},
	{
		Name: ptr.To("Standard_D4d_v4"),
	},
	{
		Name: ptr.To("Standard_D4d_v5"),
	},
	{
		Name: ptr.To("Standard_D4lds_v5"),
	},
	{
		Name: ptr.To("Standard_D4lds_v6"),
	},
	{
		Name: ptr.To("Standard_D4ls_v5"),
	},
	{
		Name: ptr.To("Standard_D4ls_v6"),
	},
	{
		Name: ptr.To("Standard_D4pds_v5"),
	},
	{
		Name: ptr.To("Standard_D4pds_v6"),
	},
	{
		Name: ptr.To("Standard_D4plds_v5"),
	},
	{
		Name: ptr.To("Standard_D4plds_v6"),
	},
	{
		Name: ptr.To("Standard_D4pls_v5"),
	},
	{
		Name: ptr.To("Standard_D4pls_v6"),
	},
	{
		Name: ptr.To("Standard_D4ps_v5"),
	},
	{
		Name: ptr.To("Standard_D4ps_v6"),
	},
	{
		Name: ptr.To("Standard_D4s_v3"),
	},
	{
		Name: ptr.To("Standard_D4s_v4"),
	},
	{
		Name: ptr.To("Standard_D4s_v5"),
	},
	{
		Name: ptr.To("Standard_D4s_v6"),
	},
	{
		Name: ptr.To("Standard_D4_v2"),
	},
	{
		Name: ptr.To("Standard_D4_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D4_v3"),
	},
	{
		Name: ptr.To("Standard_D4_v4"),
	},
	{
		Name: ptr.To("Standard_D4_v5"),
	},
	{
		Name: ptr.To("Standard_D5_v2"),
	},
	{
		Name: ptr.To("Standard_D5_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_D64ads_v5"),
	},
	{
		Name: ptr.To("Standard_D64ads_v6"),
	},
	{
		Name: ptr.To("Standard_D64alds_v6"),
	},
	{
		Name: ptr.To("Standard_D64als_v6"),
	},
	{
		Name: ptr.To("Standard_D64as_v4"),
	},
	{
		Name: ptr.To("Standard_D64as_v5"),
	},
	{
		Name: ptr.To("Standard_D64as_v6"),
	},
	{
		Name: ptr.To("Standard_D64a_v4"),
	},
	{
		Name: ptr.To("Standard_D64ds_v4"),
	},
	{
		Name: ptr.To("Standard_D64ds_v5"),
	},
	{
		Name: ptr.To("Standard_D64ds_v6"),
	},
	{
		Name: ptr.To("Standard_D64d_v4"),
	},
	{
		Name: ptr.To("Standard_D64d_v5"),
	},
	{
		Name: ptr.To("Standard_D64lds_v5"),
	},
	{
		Name: ptr.To("Standard_D64lds_v6"),
	},
	{
		Name: ptr.To("Standard_D64ls_v5"),
	},
	{
		Name: ptr.To("Standard_D64ls_v6"),
	},
	{
		Name: ptr.To("Standard_D64pds_v5"),
	},
	{
		Name: ptr.To("Standard_D64pds_v6"),
	},
	{
		Name: ptr.To("Standard_D64plds_v5"),
	},
	{
		Name: ptr.To("Standard_D64plds_v6"),
	},
	{
		Name: ptr.To("Standard_D64pls_v5"),
	},
	{
		Name: ptr.To("Standard_D64pls_v6"),
	},
	{
		Name: ptr.To("Standard_D64ps_v5"),
	},
	{
		Name: ptr.To("Standard_D64ps_v6"),
	},
	{
		Name: ptr.To("Standard_D64s_v3"),
	},
	{
		Name: ptr.To("Standard_D64s_v4"),
	},
	{
		Name: ptr.To("Standard_D64s_v5"),
	},
	{
		Name: ptr.To("Standard_D64s_v6"),
	},
	{
		Name: ptr.To("Standard_D64_v3"),
	},
	{
		Name: ptr.To("Standard_D64_v4"),
	},
	{
		Name: ptr.To("Standard_D64_v5"),
	},
	{
		Name: ptr.To("Standard_D8ads_v5"),
	},
	{
		Name: ptr.To("Standard_D8ads_v6"),
	},
	{
		Name: ptr.To("Standard_D8alds_v6"),
	},
	{
		Name: ptr.To("Standard_D8als_v6"),
	},
	{
		Name: ptr.To("Standard_D8as_v4"),
	},
	{
		Name: ptr.To("Standard_D8as_v5"),
	},
	{
		Name: ptr.To("Standard_D8as_v6"),
	},
	{
		Name: ptr.To("Standard_D8a_v4"),
	},
	{
		Name: ptr.To("Standard_D8ds_v4"),
	},
	{
		Name: ptr.To("Standard_D8ds_v5"),
	},
	{
		Name: ptr.To("Standard_D8ds_v6"),
	},
	{
		Name: ptr.To("Standard_D8d_v4"),
	},
	{
		Name: ptr.To("Standard_D8d_v5"),
	},
	{
		Name: ptr.To("Standard_D8lds_v5"),
	},
	{
		Name: ptr.To("Standard_D8lds_v6"),
	},
	{
		Name: ptr.To("Standard_D8ls_v5"),
	},
	{
		Name: ptr.To("Standard_D8ls_v6"),
	},
	{
		Name: ptr.To("Standard_D8pds_v5"),
	},
	{
		Name: ptr.To("Standard_D8pds_v6"),
	},
	{
		Name: ptr.To("Standard_D8plds_v5"),
	},
	{
		Name: ptr.To("Standard_D8plds_v6"),
	},
	{
		Name: ptr.To("Standard_D8pls_v5"),
	},
	{
		Name: ptr.To("Standard_D8pls_v6"),
	},
	{
		Name: ptr.To("Standard_D8ps_v5"),
	},
	{
		Name: ptr.To("Standard_D8ps_v6"),
	},
	{
		Name: ptr.To("Standard_D8s_v3"),
	},
	{
		Name: ptr.To("Standard_D8s_v4"),
	},
	{
		Name: ptr.To("Standard_D8s_v5"),
	},
	{
		Name: ptr.To("Standard_D8s_v6"),
	},
	{
		Name: ptr.To("Standard_D8_v3"),
	},
	{
		Name: ptr.To("Standard_D8_v4"),
	},
	{
		Name: ptr.To("Standard_D8_v5"),
	},
	{
		Name: ptr.To("Standard_D96ads_v5"),
	},
	{
		Name: ptr.To("Standard_D96ads_v6"),
	},
	{
		Name: ptr.To("Standard_D96alds_v6"),
	},
	{
		Name: ptr.To("Standard_D96als_v6"),
	},
	{
		Name: ptr.To("Standard_D96as_v4"),
	},
	{
		Name: ptr.To("Standard_D96as_v5"),
	},
	{
		Name: ptr.To("Standard_D96as_v6"),
	},
	{
		Name: ptr.To("Standard_D96a_v4"),
	},
	{
		Name: ptr.To("Standard_D96ds_v5"),
	},
	{
		Name: ptr.To("Standard_D96ds_v6"),
	},
	{
		Name: ptr.To("Standard_D96d_v5"),
	},
	{
		Name: ptr.To("Standard_D96lds_v5"),
	},
	{
		Name: ptr.To("Standard_D96lds_v6"),
	},
	{
		Name: ptr.To("Standard_D96ls_v5"),
	},
	{
		Name: ptr.To("Standard_D96ls_v6"),
	},
	{
		Name: ptr.To("Standard_D96pds_v6"),
	},
	{
		Name: ptr.To("Standard_D96plds_v6"),
	},
	{
		Name: ptr.To("Standard_D96pls_v6"),
	},
	{
		Name: ptr.To("Standard_D96ps_v6"),
	},
	{
		Name: ptr.To("Standard_D96s_v5"),
	},
	{
		Name: ptr.To("Standard_D96s_v6"),
	},
	{
		Name: ptr.To("Standard_D96_v5"),
	},
	{
		Name: ptr.To("Standard_DC16ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC16ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC16as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC16as_v5"),
	},
	{
		Name: ptr.To("Standard_DC16ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC16eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC16es_v5"),
	},
	{
		Name: ptr.To("Standard_DC16s_v3"),
	},
	{
		Name: ptr.To("Standard_DC1ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC1s_v2"),
	},
	{
		Name: ptr.To("Standard_DC1s_v3"),
	},
	{
		Name: ptr.To("Standard_DC24ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC24s_v3"),
	},
	{
		Name: ptr.To("Standard_DC2ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC2as_v5"),
	},
	{
		Name: ptr.To("Standard_DC2ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC2eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC2es_v5"),
	},
	{
		Name: ptr.To("Standard_DC2s_v2"),
	},
	{
		Name: ptr.To("Standard_DC2s_v3"),
	},
	{
		Name: ptr.To("Standard_DC32ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC32ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC32as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC32as_v5"),
	},
	{
		Name: ptr.To("Standard_DC32ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC32eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC32es_v5"),
	},
	{
		Name: ptr.To("Standard_DC32s_v3"),
	},
	{
		Name: ptr.To("Standard_DC48ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC48ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC48as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC48as_v5"),
	},
	{
		Name: ptr.To("Standard_DC48ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC48eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC48es_v5"),
	},
	{
		Name: ptr.To("Standard_DC48s_v3"),
	},
	{
		Name: ptr.To("Standard_DC4ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC4ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC4as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC4as_v5"),
	},
	{
		Name: ptr.To("Standard_DC4ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC4eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC4es_v5"),
	},
	{
		Name: ptr.To("Standard_DC4s_v2"),
	},
	{
		Name: ptr.To("Standard_DC4s_v3"),
	},
	{
		Name: ptr.To("Standard_DC64ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC64ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC64as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC64as_v5"),
	},
	{
		Name: ptr.To("Standard_DC64eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC64es_v5"),
	},
	{
		Name: ptr.To("Standard_DC8ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC8ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC8as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC8as_v5"),
	},
	{
		Name: ptr.To("Standard_DC8ds_v3"),
	},
	{
		Name: ptr.To("Standard_DC8eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC8es_v5"),
	},
	{
		Name: ptr.To("Standard_DC8s_v3"),
	},
	{
		Name: ptr.To("Standard_DC8_v2"),
	},
	{
		Name: ptr.To("Standard_DC96ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC96ads_v5"),
	},
	{
		Name: ptr.To("Standard_DC96as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_DC96as_v5"),
	},
	{
		Name: ptr.To("Standard_DC96eds_v5"),
	},
	{
		Name: ptr.To("Standard_DC96es_v5"),
	},
	{
		Name: ptr.To("Standard_DS1"),
	},
	{
		Name: ptr.To("Standard_DS11"),
	},
	{
		Name: ptr.To("Standard_DS11-1_v2"),
	},
	{
		Name: ptr.To("Standard_DS11_v2"),
	},
	{
		Name: ptr.To("Standard_DS11_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS12"),
	},
	{
		Name: ptr.To("Standard_DS12-1_v2"),
	},
	{
		Name: ptr.To("Standard_DS12-2_v2"),
	},
	{
		Name: ptr.To("Standard_DS12_v2"),
	},
	{
		Name: ptr.To("Standard_DS12_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS13"),
	},
	{
		Name: ptr.To("Standard_DS13-2_v2"),
	},
	{
		Name: ptr.To("Standard_DS13-4_v2"),
	},
	{
		Name: ptr.To("Standard_DS13_v2"),
	},
	{
		Name: ptr.To("Standard_DS13_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS14"),
	},
	{
		Name: ptr.To("Standard_DS14-4_v2"),
	},
	{
		Name: ptr.To("Standard_DS14-8_v2"),
	},
	{
		Name: ptr.To("Standard_DS14_v2"),
	},
	{
		Name: ptr.To("Standard_DS14_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS15_v2"),
	},
	{
		Name: ptr.To("Standard_DS1_v2"),
	},
	{
		Name: ptr.To("Standard_DS2"),
	},
	{
		Name: ptr.To("Standard_DS2_v2"),
	},
	{
		Name: ptr.To("Standard_DS2_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS3"),
	},
	{
		Name: ptr.To("Standard_DS3_v2"),
	},
	{
		Name: ptr.To("Standard_DS3_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS4"),
	},
	{
		Name: ptr.To("Standard_DS4_v2"),
	},
	{
		Name: ptr.To("Standard_DS4_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_DS5_v2"),
	},
	{
		Name: ptr.To("Standard_DS5_v2_Promo"),
	},
	{
		Name: ptr.To("Standard_E104ids_v5"),
	},
	{
		Name: ptr.To("Standard_E104id_v5"),
	},
	{
		Name: ptr.To("Standard_E104is_v5"),
	},
	{
		Name: ptr.To("Standard_E104i_v5"),
	},
	{
		Name: ptr.To("Standard_E112iads_v5"),
	},
	{
		Name: ptr.To("Standard_E112ias_v5"),
	},
	{
		Name: ptr.To("Standard_E112ibds_v5"),
	},
	{
		Name: ptr.To("Standard_E112ibs_v5"),
	},
	{
		Name: ptr.To("Standard_E16-4ads_v5"),
	},
	{
		Name: ptr.To("Standard_E16-4as_v4"),
	},
	{
		Name: ptr.To("Standard_E16-4as_v5"),
	},
	{
		Name: ptr.To("Standard_E16-4ds_v4"),
	},
	{
		Name: ptr.To("Standard_E16-4ds_v5"),
	},
	{
		Name: ptr.To("Standard_E16-4ds_v6"),
	},
	{
		Name: ptr.To("Standard_E16-4s_v3"),
	},
	{
		Name: ptr.To("Standard_E16-4s_v4"),
	},
	{
		Name: ptr.To("Standard_E16-4s_v5"),
	},
	{
		Name: ptr.To("Standard_E16-4s_v6"),
	},
	{
		Name: ptr.To("Standard_E16-8ads_v5"),
	},
	{
		Name: ptr.To("Standard_E16-8as_v4"),
	},
	{
		Name: ptr.To("Standard_E16-8as_v5"),
	},
	{
		Name: ptr.To("Standard_E16-8ds_v4"),
	},
	{
		Name: ptr.To("Standard_E16-8ds_v5"),
	},
	{
		Name: ptr.To("Standard_E16-8ds_v6"),
	},
	{
		Name: ptr.To("Standard_E16-8s_v3"),
	},
	{
		Name: ptr.To("Standard_E16-8s_v4"),
	},
	{
		Name: ptr.To("Standard_E16-8s_v5"),
	},
	{
		Name: ptr.To("Standard_E16-8s_v6"),
	},
	{
		Name: ptr.To("Standard_E16ads_v5"),
	},
	{
		Name: ptr.To("Standard_E16ads_v6"),
	},
	{
		Name: ptr.To("Standard_E16as_v4"),
	},
	{
		Name: ptr.To("Standard_E16as_v5"),
	},
	{
		Name: ptr.To("Standard_E16as_v6"),
	},
	{
		Name: ptr.To("Standard_E16a_v4"),
	},
	{
		Name: ptr.To("Standard_E16bds_v5"),
	},
	{
		Name: ptr.To("Standard_E16bs_v5"),
	},
	{
		Name: ptr.To("Standard_E16ds_v4"),
	},
	{
		Name: ptr.To("Standard_E16ds_v5"),
	},
	{
		Name: ptr.To("Standard_E16ds_v6"),
	},
	{
		Name: ptr.To("Standard_E16d_v4"),
	},
	{
		Name: ptr.To("Standard_E16d_v5"),
	},
	{
		Name: ptr.To("Standard_E16pds_v5"),
	},
	{
		Name: ptr.To("Standard_E16pds_v6"),
	},
	{
		Name: ptr.To("Standard_E16ps_v5"),
	},
	{
		Name: ptr.To("Standard_E16ps_v6"),
	},
	{
		Name: ptr.To("Standard_E16s_v3"),
	},
	{
		Name: ptr.To("Standard_E16s_v4"),
	},
	{
		Name: ptr.To("Standard_E16s_v5"),
	},
	{
		Name: ptr.To("Standard_E16s_v6"),
	},
	{
		Name: ptr.To("Standard_E16_v3"),
	},
	{
		Name: ptr.To("Standard_E16_v4"),
	},
	{
		Name: ptr.To("Standard_E16_v5"),
	},
	{
		Name: ptr.To("Standard_E20ads_v5"),
	},
	{
		Name: ptr.To("Standard_E20ads_v6"),
	},
	{
		Name: ptr.To("Standard_E20as_v4"),
	},
	{
		Name: ptr.To("Standard_E20as_v5"),
	},
	{
		Name: ptr.To("Standard_E20as_v6"),
	},
	{
		Name: ptr.To("Standard_E20a_v4"),
	},
	{
		Name: ptr.To("Standard_E20ds_v4"),
	},
	{
		Name: ptr.To("Standard_E20ds_v5"),
	},
	{
		Name: ptr.To("Standard_E20ds_v6"),
	},
	{
		Name: ptr.To("Standard_E20d_v4"),
	},
	{
		Name: ptr.To("Standard_E20d_v5"),
	},
	{
		Name: ptr.To("Standard_E20pds_v5"),
	},
	{
		Name: ptr.To("Standard_E20ps_v5"),
	},
	{
		Name: ptr.To("Standard_E20s_v3"),
	},
	{
		Name: ptr.To("Standard_E20s_v4"),
	},
	{
		Name: ptr.To("Standard_E20s_v5"),
	},
	{
		Name: ptr.To("Standard_E20s_v6"),
	},
	{
		Name: ptr.To("Standard_E20_v3"),
	},
	{
		Name: ptr.To("Standard_E20_v4"),
	},
	{
		Name: ptr.To("Standard_E20_v5"),
	},
	{
		Name: ptr.To("Standard_E2ads_v5"),
	},
	{
		Name: ptr.To("Standard_E2ads_v6"),
	},
	{
		Name: ptr.To("Standard_E2as_v4"),
	},
	{
		Name: ptr.To("Standard_E2as_v5"),
	},
	{
		Name: ptr.To("Standard_E2as_v6"),
	},
	{
		Name: ptr.To("Standard_E2a_v4"),
	},
	{
		Name: ptr.To("Standard_E2bds_v5"),
	},
	{
		Name: ptr.To("Standard_E2bs_v5"),
	},
	{
		Name: ptr.To("Standard_E2ds_v4"),
	},
	{
		Name: ptr.To("Standard_E2ds_v5"),
	},
	{
		Name: ptr.To("Standard_E2ds_v6"),
	},
	{
		Name: ptr.To("Standard_E2d_v4"),
	},
	{
		Name: ptr.To("Standard_E2d_v5"),
	},
	{
		Name: ptr.To("Standard_E2pds_v5"),
	},
	{
		Name: ptr.To("Standard_E2pds_v6"),
	},
	{
		Name: ptr.To("Standard_E2ps_v5"),
	},
	{
		Name: ptr.To("Standard_E2ps_v6"),
	},
	{
		Name: ptr.To("Standard_E2s_v3"),
	},
	{
		Name: ptr.To("Standard_E2s_v4"),
	},
	{
		Name: ptr.To("Standard_E2s_v5"),
	},
	{
		Name: ptr.To("Standard_E2s_v6"),
	},
	{
		Name: ptr.To("Standard_E2_v3"),
	},
	{
		Name: ptr.To("Standard_E2_v4"),
	},
	{
		Name: ptr.To("Standard_E2_v5"),
	},
	{
		Name: ptr.To("Standard_E32-16ads_v5"),
	},
	{
		Name: ptr.To("Standard_E32-16as_v4"),
	},
	{
		Name: ptr.To("Standard_E32-16as_v5"),
	},
	{
		Name: ptr.To("Standard_E32-16ds_v4"),
	},
	{
		Name: ptr.To("Standard_E32-16ds_v5"),
	},
	{
		Name: ptr.To("Standard_E32-16ds_v6"),
	},
	{
		Name: ptr.To("Standard_E32-16s_v3"),
	},
	{
		Name: ptr.To("Standard_E32-16s_v4"),
	},
	{
		Name: ptr.To("Standard_E32-16s_v5"),
	},
	{
		Name: ptr.To("Standard_E32-16s_v6"),
	},
	{
		Name: ptr.To("Standard_E32-8ads_v5"),
	},
	{
		Name: ptr.To("Standard_E32-8as_v4"),
	},
	{
		Name: ptr.To("Standard_E32-8as_v5"),
	},
	{
		Name: ptr.To("Standard_E32-8ds_v4"),
	},
	{
		Name: ptr.To("Standard_E32-8ds_v5"),
	},
	{
		Name: ptr.To("Standard_E32-8ds_v6"),
	},
	{
		Name: ptr.To("Standard_E32-8s_v3"),
	},
	{
		Name: ptr.To("Standard_E32-8s_v4"),
	},
	{
		Name: ptr.To("Standard_E32-8s_v5"),
	},
	{
		Name: ptr.To("Standard_E32-8s_v6"),
	},
	{
		Name: ptr.To("Standard_E32ads_v5"),
	},
	{
		Name: ptr.To("Standard_E32ads_v6"),
	},
	{
		Name: ptr.To("Standard_E32as_v4"),
	},
	{
		Name: ptr.To("Standard_E32as_v5"),
	},
	{
		Name: ptr.To("Standard_E32as_v6"),
	},
	{
		Name: ptr.To("Standard_E32a_v4"),
	},
	{
		Name: ptr.To("Standard_E32bds_v5"),
	},
	{
		Name: ptr.To("Standard_E32bs_v5"),
	},
	{
		Name: ptr.To("Standard_E32ds_v4"),
	},
	{
		Name: ptr.To("Standard_E32ds_v5"),
	},
	{
		Name: ptr.To("Standard_E32ds_v6"),
	},
	{
		Name: ptr.To("Standard_E32d_v4"),
	},
	{
		Name: ptr.To("Standard_E32d_v5"),
	},
	{
		Name: ptr.To("Standard_E32pds_v5"),
	},
	{
		Name: ptr.To("Standard_E32pds_v6"),
	},
	{
		Name: ptr.To("Standard_E32ps_v5"),
	},
	{
		Name: ptr.To("Standard_E32ps_v6"),
	},
	{
		Name: ptr.To("Standard_E32s_v3"),
	},
	{
		Name: ptr.To("Standard_E32s_v4"),
	},
	{
		Name: ptr.To("Standard_E32s_v5"),
	},
	{
		Name: ptr.To("Standard_E32s_v6"),
	},
	{
		Name: ptr.To("Standard_E32_v3"),
	},
	{
		Name: ptr.To("Standard_E32_v4"),
	},
	{
		Name: ptr.To("Standard_E32_v5"),
	},
	{
		Name: ptr.To("Standard_E4-2ads_v5"),
	},
	{
		Name: ptr.To("Standard_E4-2as_v4"),
	},
	{
		Name: ptr.To("Standard_E4-2as_v5"),
	},
	{
		Name: ptr.To("Standard_E4-2ds_v4"),
	},
	{
		Name: ptr.To("Standard_E4-2ds_v5"),
	},
	{
		Name: ptr.To("Standard_E4-2ds_v6"),
	},
	{
		Name: ptr.To("Standard_E4-2s_v3"),
	},
	{
		Name: ptr.To("Standard_E4-2s_v4"),
	},
	{
		Name: ptr.To("Standard_E4-2s_v5"),
	},
	{
		Name: ptr.To("Standard_E4-2s_v6"),
	},
	{
		Name: ptr.To("Standard_E48ads_v5"),
	},
	{
		Name: ptr.To("Standard_E48ads_v6"),
	},
	{
		Name: ptr.To("Standard_E48as_v4"),
	},
	{
		Name: ptr.To("Standard_E48as_v5"),
	},
	{
		Name: ptr.To("Standard_E48as_v6"),
	},
	{
		Name: ptr.To("Standard_E48a_v4"),
	},
	{
		Name: ptr.To("Standard_E48bds_v5"),
	},
	{
		Name: ptr.To("Standard_E48bs_v5"),
	},
	{
		Name: ptr.To("Standard_E48ds_v4"),
	},
	{
		Name: ptr.To("Standard_E48ds_v5"),
	},
	{
		Name: ptr.To("Standard_E48ds_v6"),
	},
	{
		Name: ptr.To("Standard_E48d_v4"),
	},
	{
		Name: ptr.To("Standard_E48d_v5"),
	},
	{
		Name: ptr.To("Standard_E48pds_v6"),
	},
	{
		Name: ptr.To("Standard_E48ps_v6"),
	},
	{
		Name: ptr.To("Standard_E48s_v3"),
	},
	{
		Name: ptr.To("Standard_E48s_v4"),
	},
	{
		Name: ptr.To("Standard_E48s_v5"),
	},
	{
		Name: ptr.To("Standard_E48s_v6"),
	},
	{
		Name: ptr.To("Standard_E48_v3"),
	},
	{
		Name: ptr.To("Standard_E48_v4"),
	},
	{
		Name: ptr.To("Standard_E48_v5"),
	},
	{
		Name: ptr.To("Standard_E4ads_v5"),
	},
	{
		Name: ptr.To("Standard_E4ads_v6"),
	},
	{
		Name: ptr.To("Standard_E4as_v4"),
	},
	{
		Name: ptr.To("Standard_E4as_v5"),
	},
	{
		Name: ptr.To("Standard_E4as_v6"),
	},
	{
		Name: ptr.To("Standard_E4a_v4"),
	},
	{
		Name: ptr.To("Standard_E4bds_v5"),
	},
	{
		Name: ptr.To("Standard_E4bs_v5"),
	},
	{
		Name: ptr.To("Standard_E4ds_v4"),
	},
	{
		Name: ptr.To("Standard_E4ds_v5"),
	},
	{
		Name: ptr.To("Standard_E4ds_v6"),
	},
	{
		Name: ptr.To("Standard_E4d_v4"),
	},
	{
		Name: ptr.To("Standard_E4d_v5"),
	},
	{
		Name: ptr.To("Standard_E4pds_v5"),
	},
	{
		Name: ptr.To("Standard_E4pds_v6"),
	},
	{
		Name: ptr.To("Standard_E4ps_v5"),
	},
	{
		Name: ptr.To("Standard_E4ps_v6"),
	},
	{
		Name: ptr.To("Standard_E4s_v3"),
	},
	{
		Name: ptr.To("Standard_E4s_v4"),
	},
	{
		Name: ptr.To("Standard_E4s_v5"),
	},
	{
		Name: ptr.To("Standard_E4s_v6"),
	},
	{
		Name: ptr.To("Standard_E4_v3"),
	},
	{
		Name: ptr.To("Standard_E4_v4"),
	},
	{
		Name: ptr.To("Standard_E4_v5"),
	},
	{
		Name: ptr.To("Standard_E64-16ads_v5"),
	},
	{
		Name: ptr.To("Standard_E64-16as_v4"),
	},
	{
		Name: ptr.To("Standard_E64-16as_v5"),
	},
	{
		Name: ptr.To("Standard_E64-16ds_v4"),
	},
	{
		Name: ptr.To("Standard_E64-16ds_v5"),
	},
	{
		Name: ptr.To("Standard_E64-16ds_v6"),
	},
	{
		Name: ptr.To("Standard_E64-16s_v3"),
	},
	{
		Name: ptr.To("Standard_E64-16s_v4"),
	},
	{
		Name: ptr.To("Standard_E64-16s_v5"),
	},
	{
		Name: ptr.To("Standard_E64-16s_v6"),
	},
	{
		Name: ptr.To("Standard_E64-32ads_v5"),
	},
	{
		Name: ptr.To("Standard_E64-32as_v4"),
	},
	{
		Name: ptr.To("Standard_E64-32as_v5"),
	},
	{
		Name: ptr.To("Standard_E64-32ds_v4"),
	},
	{
		Name: ptr.To("Standard_E64-32ds_v5"),
	},
	{
		Name: ptr.To("Standard_E64-32ds_v6"),
	},
	{
		Name: ptr.To("Standard_E64-32s_v3"),
	},
	{
		Name: ptr.To("Standard_E64-32s_v4"),
	},
	{
		Name: ptr.To("Standard_E64-32s_v5"),
	},
	{
		Name: ptr.To("Standard_E64-32s_v6"),
	},
	{
		Name: ptr.To("Standard_E64ads_v5"),
	},
	{
		Name: ptr.To("Standard_E64ads_v6"),
	},
	{
		Name: ptr.To("Standard_E64as_v4"),
	},
	{
		Name: ptr.To("Standard_E64as_v5"),
	},
	{
		Name: ptr.To("Standard_E64as_v6"),
	},
	{
		Name: ptr.To("Standard_E64a_v4"),
	},
	{
		Name: ptr.To("Standard_E64bds_v5"),
	},
	{
		Name: ptr.To("Standard_E64bs_v5"),
	},
	{
		Name: ptr.To("Standard_E64ds_v4"),
	},
	{
		Name: ptr.To("Standard_E64ds_v5"),
	},
	{
		Name: ptr.To("Standard_E64ds_v6"),
	},
	{
		Name: ptr.To("Standard_E64d_v4"),
	},
	{
		Name: ptr.To("Standard_E64d_v5"),
	},
	{
		Name: ptr.To("Standard_E64is_v3"),
	},
	{
		Name: ptr.To("Standard_E64i_v3"),
	},
	{
		Name: ptr.To("Standard_E64pds_v6"),
	},
	{
		Name: ptr.To("Standard_E64ps_v6"),
	},
	{
		Name: ptr.To("Standard_E64s_v3"),
	},
	{
		Name: ptr.To("Standard_E64s_v4"),
	},
	{
		Name: ptr.To("Standard_E64s_v5"),
	},
	{
		Name: ptr.To("Standard_E64s_v6"),
	},
	{
		Name: ptr.To("Standard_E64_v3"),
	},
	{
		Name: ptr.To("Standard_E64_v4"),
	},
	{
		Name: ptr.To("Standard_E64_v5"),
	},
	{
		Name: ptr.To("Standard_E8-2ads_v5"),
	},
	{
		Name: ptr.To("Standard_E8-2as_v4"),
	},
	{
		Name: ptr.To("Standard_E8-2as_v5"),
	},
	{
		Name: ptr.To("Standard_E8-2ds_v4"),
	},
	{
		Name: ptr.To("Standard_E8-2ds_v5"),
	},
	{
		Name: ptr.To("Standard_E8-2ds_v6"),
	},
	{
		Name: ptr.To("Standard_E8-2s_v3"),
	},
	{
		Name: ptr.To("Standard_E8-2s_v4"),
	},
	{
		Name: ptr.To("Standard_E8-2s_v5"),
	},
	{
		Name: ptr.To("Standard_E8-2s_v6"),
	},
	{
		Name: ptr.To("Standard_E8-4ads_v5"),
	},
	{
		Name: ptr.To("Standard_E8-4as_v4"),
	},
	{
		Name: ptr.To("Standard_E8-4as_v5"),
	},
	{
		Name: ptr.To("Standard_E8-4ds_v4"),
	},
	{
		Name: ptr.To("Standard_E8-4ds_v5"),
	},
	{
		Name: ptr.To("Standard_E8-4ds_v6"),
	},
	{
		Name: ptr.To("Standard_E8-4s_v3"),
	},
	{
		Name: ptr.To("Standard_E8-4s_v4"),
	},
	{
		Name: ptr.To("Standard_E8-4s_v5"),
	},
	{
		Name: ptr.To("Standard_E8-4s_v6"),
	},
	{
		Name: ptr.To("Standard_E80ids_v4"),
	},
	{
		Name: ptr.To("Standard_E80is_v4"),
	},
	{
		Name: ptr.To("Standard_E8ads_v5"),
	},
	{
		Name: ptr.To("Standard_E8ads_v6"),
	},
	{
		Name: ptr.To("Standard_E8as_v4"),
	},
	{
		Name: ptr.To("Standard_E8as_v5"),
	},
	{
		Name: ptr.To("Standard_E8as_v6"),
	},
	{
		Name: ptr.To("Standard_E8a_v4"),
	},
	{
		Name: ptr.To("Standard_E8bds_v5"),
	},
	{
		Name: ptr.To("Standard_E8bs_v5"),
	},
	{
		Name: ptr.To("Standard_E8ds_v4"),
	},
	{
		Name: ptr.To("Standard_E8ds_v5"),
	},
	{
		Name: ptr.To("Standard_E8ds_v6"),
	},
	{
		Name: ptr.To("Standard_E8d_v4"),
	},
	{
		Name: ptr.To("Standard_E8d_v5"),
	},
	{
		Name: ptr.To("Standard_E8pds_v5"),
	},
	{
		Name: ptr.To("Standard_E8pds_v6"),
	},
	{
		Name: ptr.To("Standard_E8ps_v5"),
	},
	{
		Name: ptr.To("Standard_E8ps_v6"),
	},
	{
		Name: ptr.To("Standard_E8s_v3"),
	},
	{
		Name: ptr.To("Standard_E8s_v4"),
	},
	{
		Name: ptr.To("Standard_E8s_v5"),
	},
	{
		Name: ptr.To("Standard_E8s_v6"),
	},
	{
		Name: ptr.To("Standard_E8_v3"),
	},
	{
		Name: ptr.To("Standard_E8_v4"),
	},
	{
		Name: ptr.To("Standard_E8_v5"),
	},
	{
		Name: ptr.To("Standard_E96-24ads_v5"),
	},
	{
		Name: ptr.To("Standard_E96-24ads_v6"),
	},
	{
		Name: ptr.To("Standard_E96-24as_v4"),
	},
	{
		Name: ptr.To("Standard_E96-24as_v5"),
	},
	{
		Name: ptr.To("Standard_E96-24ds_v5"),
	},
	{
		Name: ptr.To("Standard_E96-24ds_v6"),
	},
	{
		Name: ptr.To("Standard_E96-24s_v5"),
	},
	{
		Name: ptr.To("Standard_E96-24s_v6"),
	},
	{
		Name: ptr.To("Standard_E96-48ads_v5"),
	},
	{
		Name: ptr.To("Standard_E96-48ads_v6"),
	},
	{
		Name: ptr.To("Standard_E96-48as_v4"),
	},
	{
		Name: ptr.To("Standard_E96-48as_v5"),
	},
	{
		Name: ptr.To("Standard_E96-48ds_v5"),
	},
	{
		Name: ptr.To("Standard_E96-48ds_v6"),
	},
	{
		Name: ptr.To("Standard_E96-48s_v5"),
	},
	{
		Name: ptr.To("Standard_E96-48s_v6"),
	},
	{
		Name: ptr.To("Standard_E96ads_v5"),
	},
	{
		Name: ptr.To("Standard_E96ads_v6"),
	},
	{
		Name: ptr.To("Standard_E96as_v4"),
	},
	{
		Name: ptr.To("Standard_E96as_v5"),
	},
	{
		Name: ptr.To("Standard_E96as_v6"),
	},
	{
		Name: ptr.To("Standard_E96a_v4"),
	},
	{
		Name: ptr.To("Standard_E96bds_v5"),
	},
	{
		Name: ptr.To("Standard_E96bs_v5"),
	},
	{
		Name: ptr.To("Standard_E96ds_v5"),
	},
	{
		Name: ptr.To("Standard_E96ds_v6"),
	},
	{
		Name: ptr.To("Standard_E96d_v5"),
	},
	{
		Name: ptr.To("Standard_E96ias_v4"),
	},
	{
		Name: ptr.To("Standard_E96pds_v6"),
	},
	{
		Name: ptr.To("Standard_E96ps_v6"),
	},
	{
		Name: ptr.To("Standard_E96s_v5"),
	},
	{
		Name: ptr.To("Standard_E96s_v6"),
	},
	{
		Name: ptr.To("Standard_E96_v5"),
	},
	{
		Name: ptr.To("Standard_EC128eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC128es_v5"),
	},
	{
		Name: ptr.To("Standard_EC128ieds_v5"),
	},
	{
		Name: ptr.To("Standard_EC128ies_v5"),
	},
	{
		Name: ptr.To("Standard_EC16ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC16ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC16as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC16as_v5"),
	},
	{
		Name: ptr.To("Standard_EC16eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC16es_v5"),
	},
	{
		Name: ptr.To("Standard_EC20ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC20ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC20as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC20as_v5"),
	},
	{
		Name: ptr.To("Standard_EC2ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC2as_v5"),
	},
	{
		Name: ptr.To("Standard_EC2eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC2es_v5"),
	},
	{
		Name: ptr.To("Standard_EC32ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC32ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC32as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC32as_v5"),
	},
	{
		Name: ptr.To("Standard_EC32eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC32es_v5"),
	},
	{
		Name: ptr.To("Standard_EC48ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC48ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC48as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC48as_v5"),
	},
	{
		Name: ptr.To("Standard_EC48eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC48es_v5"),
	},
	{
		Name: ptr.To("Standard_EC4ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC4ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC4as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC4as_v5"),
	},
	{
		Name: ptr.To("Standard_EC4eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC4es_v5"),
	},
	{
		Name: ptr.To("Standard_EC64ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC64ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC64as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC64as_v5"),
	},
	{
		Name: ptr.To("Standard_EC64eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC64es_v5"),
	},
	{
		Name: ptr.To("Standard_EC8ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC8ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC8as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC8as_v5"),
	},
	{
		Name: ptr.To("Standard_EC8eds_v5"),
	},
	{
		Name: ptr.To("Standard_EC8es_v5"),
	},
	{
		Name: ptr.To("Standard_EC96ads_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC96ads_v5"),
	},
	{
		Name: ptr.To("Standard_EC96as_cc_v5"),
	},
	{
		Name: ptr.To("Standard_EC96as_v5"),
	},
	{
		Name: ptr.To("Standard_EC96iads_v5"),
	},
	{
		Name: ptr.To("Standard_EC96ias_v5"),
	},
	{
		Name: ptr.To("Standard_F1"),
	},
	{
		Name: ptr.To("Standard_F16"),
	},
	{
		Name: ptr.To("Standard_F16als_v6"),
	},
	{
		Name: ptr.To("Standard_F16ams_v6"),
	},
	{
		Name: ptr.To("Standard_F16as_v6"),
	},
	{
		Name: ptr.To("Standard_F16s"),
	},
	{
		Name: ptr.To("Standard_F16s_v2"),
	},
	{
		Name: ptr.To("Standard_F1s"),
	},
	{
		Name: ptr.To("Standard_F2"),
	},
	{
		Name: ptr.To("Standard_F2als_v6"),
	},
	{
		Name: ptr.To("Standard_F2ams_v6"),
	},
	{
		Name: ptr.To("Standard_F2as_v6"),
	},
	{
		Name: ptr.To("Standard_F2s"),
	},
	{
		Name: ptr.To("Standard_F2s_v2"),
	},
	{
		Name: ptr.To("Standard_F32als_v6"),
	},
	{
		Name: ptr.To("Standard_F32ams_v6"),
	},
	{
		Name: ptr.To("Standard_F32as_v6"),
	},
	{
		Name: ptr.To("Standard_F32s_v2"),
	},
	{
		Name: ptr.To("Standard_F4"),
	},
	{
		Name: ptr.To("Standard_F48als_v6"),
	},
	{
		Name: ptr.To("Standard_F48ams_v6"),
	},
	{
		Name: ptr.To("Standard_F48as_v6"),
	},
	{
		Name: ptr.To("Standard_F48s_v2"),
	},
	{
		Name: ptr.To("Standard_F4als_v6"),
	},
	{
		Name: ptr.To("Standard_F4ams_v6"),
	},
	{
		Name: ptr.To("Standard_F4as_v6"),
	},
	{
		Name: ptr.To("Standard_F4s"),
	},
	{
		Name: ptr.To("Standard_F4s_v2"),
	},
	{
		Name: ptr.To("Standard_F64als_v6"),
	},
	{
		Name: ptr.To("Standard_F64ams_v6"),
	},
	{
		Name: ptr.To("Standard_F64as_v6"),
	},
	{
		Name: ptr.To("Standard_F64s_v2"),
	},
	{
		Name: ptr.To("Standard_F72s_v2"),
	},
	{
		Name: ptr.To("Standard_F8"),
	},
	{
		Name: ptr.To("Standard_F8als_v6"),
	},
	{
		Name: ptr.To("Standard_F8ams_v6"),
	},
	{
		Name: ptr.To("Standard_F8as_v6"),
	},
	{
		Name: ptr.To("Standard_F8s"),
	},
	{
		Name: ptr.To("Standard_F8s_v2"),
	},
	{
		Name: ptr.To("Standard_FX12mds"),
	},
	{
		Name: ptr.To("Standard_FX24mds"),
	},
	{
		Name: ptr.To("Standard_FX36mds"),
	},
	{
		Name: ptr.To("Standard_FX48mds"),
	},
	{
		Name: ptr.To("Standard_FX4mds"),
	},
	{
		Name: ptr.To("Standard_G1"),
	},
	{
		Name: ptr.To("Standard_G2"),
	},
	{
		Name: ptr.To("Standard_G3"),
	},
	{
		Name: ptr.To("Standard_G4"),
	},
	{
		Name: ptr.To("Standard_G5"),
	},
	{
		Name: ptr.To("Standard_GS1"),
	},
	{
		Name: ptr.To("Standard_GS2"),
	},
	{
		Name: ptr.To("Standard_GS3"),
	},
	{
		Name: ptr.To("Standard_GS4"),
	},
	{
		Name: ptr.To("Standard_GS4-4"),
	},
	{
		Name: ptr.To("Standard_GS4-8"),
	},
	{
		Name: ptr.To("Standard_GS5"),
	},
	{
		Name: ptr.To("Standard_GS5-16"),
	},
	{
		Name: ptr.To("Standard_GS5-8"),
	},
	{
		Name: ptr.To("Standard_HB120-16rs_v2"),
	},
	{
		Name: ptr.To("Standard_HB120-16rs_v3"),
	},
	{
		Name: ptr.To("Standard_HB120-32rs_v2"),
	},
	{
		Name: ptr.To("Standard_HB120-32rs_v3"),
	},
	{
		Name: ptr.To("Standard_HB120-64rs_v2"),
	},
	{
		Name: ptr.To("Standard_HB120-64rs_v3"),
	},
	{
		Name: ptr.To("Standard_HB120-96rs_v2"),
	},
	{
		Name: ptr.To("Standard_HB120-96rs_v3"),
	},
	{
		Name: ptr.To("Standard_HB120rs_v2"),
	},
	{
		Name: ptr.To("Standard_HB120rs_v3"),
	},
	{
		Name: ptr.To("Standard_HB176-144rs_v4"),
	},
	{
		Name: ptr.To("Standard_HB176-24rs_v4"),
	},
	{
		Name: ptr.To("Standard_HB176-48rs_v4"),
	},
	{
		Name: ptr.To("Standard_HB176-96rs_v4"),
	},
	{
		Name: ptr.To("Standard_HB176rs_v4"),
	},
	{
		Name: ptr.To("Standard_HC44-16rs"),
	},
	{
		Name: ptr.To("Standard_HC44-32rs"),
	},
	{
		Name: ptr.To("Standard_HC44rs"),
	},
	{
		Name: ptr.To("Standard_HX176-144rs"),
	},
	{
		Name: ptr.To("Standard_HX176-24rs"),
	},
	{
		Name: ptr.To("Standard_HX176-48rs"),
	},
	{
		Name: ptr.To("Standard_HX176-96rs"),
	},
	{
		Name: ptr.To("Standard_HX176rs"),
	},
	{
		Name: ptr.To("Standard_L16as_v3"),
	},
	{
		Name: ptr.To("Standard_L16s"),
	},
	{
		Name: ptr.To("Standard_L16s_v2"),
	},
	{
		Name: ptr.To("Standard_L16s_v3"),
	},
	{
		Name: ptr.To("Standard_L32as_v3"),
	},
	{
		Name: ptr.To("Standard_L32s"),
	},
	{
		Name: ptr.To("Standard_L32s_v2"),
	},
	{
		Name: ptr.To("Standard_L32s_v3"),
	},
	{
		Name: ptr.To("Standard_L48as_v3"),
	},
	{
		Name: ptr.To("Standard_L48s_v2"),
	},
	{
		Name: ptr.To("Standard_L48s_v3"),
	},
	{
		Name: ptr.To("Standard_L4s"),
	},
	{
		Name: ptr.To("Standard_L64as_v3"),
	},
	{
		Name: ptr.To("Standard_L64s_v2"),
	},
	{
		Name: ptr.To("Standard_L64s_v3"),
	},
	{
		Name: ptr.To("Standard_L80as_v3"),
	},
	{
		Name: ptr.To("Standard_L80s_v2"),
	},
	{
		Name: ptr.To("Standard_L80s_v3"),
	},
	{
		Name: ptr.To("Standard_L8as_v3"),
	},
	{
		Name: ptr.To("Standard_L8s"),
	},
	{
		Name: ptr.To("Standard_L8s_v2"),
	},
	{
		Name: ptr.To("Standard_L8s_v3"),
	},
	{
		Name: ptr.To("Standard_M128"),
	},
	{
		Name: ptr.To("Standard_M128-32ms"),
	},
	{
		Name: ptr.To("Standard_M128-64bds_3_v3"),
	},
	{
		Name: ptr.To("Standard_M128-64bds_v3"),
	},
	{
		Name: ptr.To("Standard_M128-64bs_v3"),
	},
	{
		Name: ptr.To("Standard_M128-64ms"),
	},
	{
		Name: ptr.To("Standard_M128bds_3_v3"),
	},
	{
		Name: ptr.To("Standard_M128bds_v3"),
	},
	{
		Name: ptr.To("Standard_M128bs_v3"),
	},
	{
		Name: ptr.To("Standard_M128dms_v2"),
	},
	{
		Name: ptr.To("Standard_M128ds_v2"),
	},
	{
		Name: ptr.To("Standard_M128m"),
	},
	{
		Name: ptr.To("Standard_M128ms"),
	},
	{
		Name: ptr.To("Standard_M128ms_v2"),
	},
	{
		Name: ptr.To("Standard_M128s"),
	},
	{
		Name: ptr.To("Standard_M128s_v2"),
	},
	{
		Name: ptr.To("Standard_M12ds_v3"),
	},
	{
		Name: ptr.To("Standard_M12s_v3"),
	},
	{
		Name: ptr.To("Standard_M16-4ms"),
	},
	{
		Name: ptr.To("Standard_M16-8ms"),
	},
	{
		Name: ptr.To("Standard_M16bds_v3"),
	},
	{
		Name: ptr.To("Standard_M16bs_v3"),
	},
	{
		Name: ptr.To("Standard_M16ms"),
	},
	{
		Name: ptr.To("Standard_M176-88bds_4_v3"),
	},
	{
		Name: ptr.To("Standard_M176-88bds_v3"),
	},
	{
		Name: ptr.To("Standard_M176-88bs_v3"),
	},
	{
		Name: ptr.To("Standard_M176bds_4_v3"),
	},
	{
		Name: ptr.To("Standard_M176bds_v3"),
	},
	{
		Name: ptr.To("Standard_M176bs_v3"),
	},
	{
		Name: ptr.To("Standard_M176ds_3_v3"),
	},
	{
		Name: ptr.To("Standard_M176ds_4_v3"),
	},
	{
		Name: ptr.To("Standard_M176s_3_v3"),
	},
	{
		Name: ptr.To("Standard_M176s_4_v3"),
	},
	{
		Name: ptr.To("Standard_M192idms_v2"),
	},
	{
		Name: ptr.To("Standard_M192ids_v2"),
	},
	{
		Name: ptr.To("Standard_M192ims_v2"),
	},
	{
		Name: ptr.To("Standard_M192is_v2"),
	},
	{
		Name: ptr.To("Standard_M208ms_v2"),
	},
	{
		Name: ptr.To("Standard_M208s_v2"),
	},
	{
		Name: ptr.To("Standard_M24ds_v3"),
	},
	{
		Name: ptr.To("Standard_M24s_v3"),
	},
	{
		Name: ptr.To("Standard_M32-16ms"),
	},
	{
		Name: ptr.To("Standard_M32-8ms"),
	},
	{
		Name: ptr.To("Standard_M32bds_v3"),
	},
	{
		Name: ptr.To("Standard_M32bs_v3"),
	},
	{
		Name: ptr.To("Standard_M32dms_v2"),
	},
	{
		Name: ptr.To("Standard_M32ls"),
	},
	{
		Name: ptr.To("Standard_M32ms"),
	},
	{
		Name: ptr.To("Standard_M32ms_v2"),
	},
	{
		Name: ptr.To("Standard_M32ts"),
	},
	{
		Name: ptr.To("Standard_M416-208ms_v2"),
	},
	{
		Name: ptr.To("Standard_M416-208s_v2"),
	},
	{
		Name: ptr.To("Standard_M416ds_6_v3"),
	},
	{
		Name: ptr.To("Standard_M416ds_8_v3"),
	},
	{
		Name: ptr.To("Standard_M416ms_v2"),
	},
	{
		Name: ptr.To("Standard_M416s_10_v2"),
	},
	{
		Name: ptr.To("Standard_M416s_6_v3"),
	},
	{
		Name: ptr.To("Standard_M416s_8_v2"),
	},
	{
		Name: ptr.To("Standard_M416s_8_v3"),
	},
	{
		Name: ptr.To("Standard_M416s_9_v2"),
	},
	{
		Name: ptr.To("Standard_M416s_v2"),
	},
	{
		Name: ptr.To("Standard_M48bds_v3"),
	},
	{
		Name: ptr.To("Standard_M48bs_v3"),
	},
	{
		Name: ptr.To("Standard_M48ds_1_v3"),
	},
	{
		Name: ptr.To("Standard_M48s_1_v3"),
	},
	{
		Name: ptr.To("Standard_M624ds_12_v3"),
	},
	{
		Name: ptr.To("Standard_M624s_12_v3"),
	},
	{
		Name: ptr.To("Standard_M64"),
	},
	{
		Name: ptr.To("Standard_M64-16ms"),
	},
	{
		Name: ptr.To("Standard_M64-32bds_1_v3"),
	},
	{
		Name: ptr.To("Standard_M64-32ms"),
	},
	{
		Name: ptr.To("Standard_M64bds_1_v3"),
	},
	{
		Name: ptr.To("Standard_M64bds_v3"),
	},
	{
		Name: ptr.To("Standard_M64bs_v3"),
	},
	{
		Name: ptr.To("Standard_M64dms_v2"),
	},
	{
		Name: ptr.To("Standard_M64ds_v2"),
	},
	{
		Name: ptr.To("Standard_M64ls"),
	},
	{
		Name: ptr.To("Standard_M64m"),
	},
	{
		Name: ptr.To("Standard_M64ms"),
	},
	{
		Name: ptr.To("Standard_M64ms_v2"),
	},
	{
		Name: ptr.To("Standard_M64s"),
	},
	{
		Name: ptr.To("Standard_M64s_v2"),
	},
	{
		Name: ptr.To("Standard_M8-2ms"),
	},
	{
		Name: ptr.To("Standard_M8-4ms"),
	},
	{
		Name: ptr.To("Standard_M832ds_12_v3"),
	},
	{
		Name: ptr.To("Standard_M832ids_16_v3"),
	},
	{
		Name: ptr.To("Standard_M832is_16_v3"),
	},
	{
		Name: ptr.To("Standard_M832s_12_v3"),
	},
	{
		Name: ptr.To("Standard_M8ms"),
	},
	{
		Name: ptr.To("Standard_M96-48bds_2_v3"),
	},
	{
		Name: ptr.To("Standard_M96bds_2_v3"),
	},
	{
		Name: ptr.To("Standard_M96bds_v3"),
	},
	{
		Name: ptr.To("Standard_M96bs_v3"),
	},
	{
		Name: ptr.To("Standard_M96ds_1_v3"),
	},
	{
		Name: ptr.To("Standard_M96ds_2_v3"),
	},
	{
		Name: ptr.To("Standard_M96s_1_v3"),
	},
	{
		Name: ptr.To("Standard_M96s_2_v3"),
	},
	{
		Name: ptr.To("Standard_NC12s_v3"),
	},
	{
		Name: ptr.To("Standard_NC16ads_A10_v4"),
	},
	{
		Name: ptr.To("Standard_NC16as_T4_v3"),
	},
	{
		Name: ptr.To("Standard_NC24ads_A100_v4"),
	},
	{
		Name: ptr.To("Standard_NC24rs_v3"),
	},
	{
		Name: ptr.To("Standard_NC24s_v3"),
	},
	{
		Name: ptr.To("Standard_NC32ads_A10_v4"),
	},
	{
		Name: ptr.To("Standard_NC40ads_H100_v5"),
	},
	{
		Name: ptr.To("Standard_NC48ads_A100_v4"),
	},
	{
		Name: ptr.To("Standard_NC4as_T4_v3"),
	},
	{
		Name: ptr.To("Standard_NC64as_T4_v3"),
	},
	{
		Name: ptr.To("Standard_NC6s_v3"),
	},
	{
		Name: ptr.To("Standard_NC80adis_H100_v5"),
	},
	{
		Name: ptr.To("Standard_NC8ads_A10_v4"),
	},
	{
		Name: ptr.To("Standard_NC8as_T4_v3"),
	},
	{
		Name: ptr.To("Standard_NC96ads_A100_v4"),
	},
	{
		Name: ptr.To("Standard_NCC40ads_H100_v5"),
	},
	{
		Name: ptr.To("Standard_ND40rs_v2"),
	},
	{
		Name: ptr.To("Standard_ND40s_v3"),
	},
	{
		Name: ptr.To("Standard_ND96amsr_A100_v4"),
	},
	{
		Name: ptr.To("Standard_ND96asr_v4"),
	},
	{
		Name: ptr.To("Standard_ND96isr_H100_v5"),
	},
	{
		Name: ptr.To("Standard_ND96isr_H200_v5"),
	},
	{
		Name: ptr.To("Standard_ND96isrf_H200_v5"),
	},
	{
		Name: ptr.To("Standard_ND96isr_MI300X_v5"),
	},
	{
		Name: ptr.To("Standard_ND96is_MI300X_v5"),
	},
	{
		Name: ptr.To("Standard_NG16ads_V620_v1"),
	},
	{
		Name: ptr.To("Standard_NG32adms_V620_v1"),
	},
	{
		Name: ptr.To("Standard_NG32ads_V620_v1"),
	},
	{
		Name: ptr.To("Standard_NG8ads_V620_v1"),
	},
	{
		Name: ptr.To("Standard_NP10s"),
	},
	{
		Name: ptr.To("Standard_NP20s"),
	},
	{
		Name: ptr.To("Standard_NP40s"),
	},
	{
		Name: ptr.To("Standard_NV12ads_A10_v5"),
	},
	{
		Name: ptr.To("Standard_NV12ads_V710_v5"),
	},
	{
		Name: ptr.To("Standard_NV12s_v2"),
	},
	{
		Name: ptr.To("Standard_NV12s_v3"),
	},
	{
		Name: ptr.To("Standard_NV16as_v4"),
	},
	{
		Name: ptr.To("Standard_NV18ads_A10_v5"),
	},
	{
		Name: ptr.To("Standard_NV24ads_V710_v5"),
	},
	{
		Name: ptr.To("Standard_NV24s_v2"),
	},
	{
		Name: ptr.To("Standard_NV24s_v3"),
	},
	{
		Name: ptr.To("Standard_NV28adms_V710_v5"),
	},
	{
		Name: ptr.To("Standard_NV32as_v4"),
	},
	{
		Name: ptr.To("Standard_NV36adms_A10_v5"),
	},
	{
		Name: ptr.To("Standard_NV36ads_A10_v5"),
	},
	{
		Name: ptr.To("Standard_NV48s_v3"),
	},
	{
		Name: ptr.To("Standard_NV4ads_V710_v5"),
	},
	{
		Name: ptr.To("Standard_NV4as_v4"),
	},
	{
		Name: ptr.To("Standard_NV6ads_A10_v5"),
	},
	{
		Name: ptr.To("Standard_NV6s_v2"),
	},
	{
		Name: ptr.To("Standard_NV72ads_A10_v5"),
	},
	{
		Name: ptr.To("Standard_NV8ads_V710_v5"),
	},
	{
		Name: ptr.To("Standard_NV8as_v4"),
	},
	{
		Name: ptr.To("Standard_PB6s"),
	},
}
