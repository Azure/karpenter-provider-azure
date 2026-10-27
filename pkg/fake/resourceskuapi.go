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

package fake

import (
	"context"
	"fmt"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v9"
	"github.com/Azure/skewer/v2"
	"github.com/samber/lo"
)

// TODO: consider using fakes from skewer itself

const SyntheticRegionalSKUName = "Standard_TestRegional_v1"

// ResourceSkus is a map of location to resource skus
var ResourceSkus = make(map[string][]*armcompute.ResourceSKU)

// assert that the fake implements the interface
var _ skewer.ResourceClient = &ResourceSKUsAPI{}

type ResourceSKUsAPI struct {
	Location string
	// skewer.ResourceClient
	Error          error
	AdditionalSKUs []*armcompute.ResourceSKU
}

// Reset must be called between tests otherwise tests will pollute each other.
func (s *ResourceSKUsAPI) Reset() {
	//c.ResourceSKUsBehavior.Reset()
	s.Error = nil
	s.AdditionalSKUs = nil
}

func (s *ResourceSKUsAPI) NewListPager(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[armcompute.ResourceSKUsClientListResponse]{
		More: func(page armcompute.ResourceSKUsClientListResponse) bool {
			return page.NextLink != nil && len(*page.NextLink) > 0
		},
		Fetcher: func(ctx context.Context, page *armcompute.ResourceSKUsClientListResponse) (armcompute.ResourceSKUsClientListResponse, error) {
			if s.Error != nil {
				return armcompute.ResourceSKUsClientListResponse{}, s.Error
			}

			// First page
			if page == nil {
				resourceSkus := ResourceSkus[s.Location]
				if len(s.AdditionalSKUs) > 0 {
					resourceSkus = slices.Concat(resourceSkus, s.AdditionalSKUs)
				}
				return armcompute.ResourceSKUsClientListResponse{
					ResourceSKUsResult: armcompute.ResourceSKUsResult{
						Value: resourceSkus,
					},
				}, nil
			}

			// No more pages
			return armcompute.ResourceSKUsClientListResponse{}, nil
		},
	})
}

// MakeSyntheticRegionalSKU returns a test SKU that is available regionally but not in any availability zone.
func MakeSyntheticRegionalSKU(location string) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         lo.ToPtr(SyntheticRegionalSKUName),
		Tier:         lo.ToPtr("Standard"),
		Kind:         lo.ToPtr(""),
		Size:         lo.ToPtr("D2_v2"),
		Family:       lo.ToPtr("standardDv2Family"),
		ResourceType: lo.ToPtr("virtualMachines"),
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{Name: lo.ToPtr("OSVhdSizeMB"), Value: lo.ToPtr("1047552")},
			{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")},
			{Name: lo.ToPtr("HyperVGenerations"), Value: lo.ToPtr("V1,V2")},
			{Name: lo.ToPtr("MemoryGB"), Value: lo.ToPtr("8")},
			{Name: lo.ToPtr("MaxDataDiskCount"), Value: lo.ToPtr("8")},
			{Name: lo.ToPtr("CpuArchitectureType"), Value: lo.ToPtr("x64")},
			{Name: lo.ToPtr("LowPriorityCapable"), Value: lo.ToPtr("True")},
			{Name: lo.ToPtr("PremiumIO"), Value: lo.ToPtr("True")},
			{Name: lo.ToPtr("VMDeploymentTypes"), Value: lo.ToPtr("IaaS")},
			{Name: lo.ToPtr("EphemeralOSDiskSupported"), Value: lo.ToPtr("False")},
			{Name: lo.ToPtr("EncryptionAtHostSupported"), Value: lo.ToPtr("True")},
			{Name: lo.ToPtr("AcceleratedNetworkingEnabled"), Value: lo.ToPtr("True")},
			{Name: lo.ToPtr("RdmaEnabled"), Value: lo.ToPtr("False")},
			{Name: lo.ToPtr("MaxNetworkInterfaces"), Value: lo.ToPtr("4")},
		},
		Locations: []*string{lo.ToPtr(location)},
		LocationInfo: []*armcompute.ResourceSKULocationInfo{{
			Location: lo.ToPtr(location),
			Zones:    []*string{},
		}},
	}
}

// MakeSKU looks up a full *skewer.SKU from the fake ResourceSkus data for the default Region.
// This includes Name, Family, Capabilities (vCPU count, etc.), and other SKU metadata.
// Panics if the SKU is not found in the fake data.
func MakeSKU(skuName string) *skewer.SKU {
	return MakeSKUForRegion(skuName, Region)
}

// MakeSKUForRegion looks up a full *skewer.SKU from the fake ResourceSkus data for the given region.
// This includes Name, Family, Capabilities (vCPU count, etc.), and other SKU metadata.
// Panics if the SKU is not found in the fake data.
func MakeSKUForRegion(skuName, region string) *skewer.SKU {
	data := ResourceSkus[region]
	for _, sku := range data {
		if lo.FromPtr(sku.Name) == skuName {
			return &skewer.SKU{
				Name:         sku.Name,
				Capabilities: sku.Capabilities,
				Locations:    sku.Locations,
				Family:       sku.Family,
				Size:         sku.Size,
				ResourceType: sku.ResourceType,
			}
		}
	}
	panic(fmt.Sprintf("SKU %q not found in fake ResourceSkus for region %q", skuName, region))
}
