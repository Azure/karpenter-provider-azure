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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/skewer/v2"
)

// TODO: consider using fakes from skewer itself

// ResourceSkus is a map of location to resource skus
var ResourceSkus = make(map[string][]*armcompute.ResourceSKU)

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
