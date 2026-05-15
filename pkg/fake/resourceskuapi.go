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

	//nolint:staticcheck // deprecated package
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"
	"github.com/Azure/skewer"
	"github.com/samber/lo"
)

// TODO: consider using fakes from skewer itself

// ResourceSkus is a map of location to resource skus
var ResourceSkus = make(map[string][]compute.ResourceSku)

// assert that the fake implements the interface
var _ skewer.ResourceClient = &ResourceSKUsAPI{}

type ResourceSKUsAPI struct {
	Location string
	// skewer.ResourceClient
	Error error
}

// Reset must be called between tests otherwise tests will pollute each other.
func (s *ResourceSKUsAPI) Reset() {
	//c.ResourceSKUsBehavior.Reset()
	s.Error = nil
}

func (s *ResourceSKUsAPI) ListComplete(_ context.Context, _, _ string) (compute.ResourceSkusResultIterator, error) {
	if s.Error != nil {
		return compute.ResourceSkusResultIterator{}, s.Error
	}
	resourceSkus := ResourceSkus[s.Location]
	return compute.NewResourceSkusResultIterator(
		compute.NewResourceSkusResultPage(
			// cur
			compute.ResourceSkusResult{
				Value: &resourceSkus,
			},
			// fn
			func(ctx context.Context, result compute.ResourceSkusResult) (compute.ResourceSkusResult, error) {
				return compute.ResourceSkusResult{
					Value:    nil, // end of iterator
					NextLink: nil,
				}, nil
			},
		),
	), nil
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
