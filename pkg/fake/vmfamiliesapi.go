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
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computelimit/armcomputelimit"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
)

type QuotaCategoryVMFamilyMappingAPI struct {
	VMFamilies AtomicPtrSlice[armcomputelimit.VMFamily]
	Error      error
	ErrorPage  int
	PageSize   int
	calls      atomic.Int64
}

var _ quota.QuotaCategoryVMFamilyMappingAPI = &QuotaCategoryVMFamilyMappingAPI{}

func (v *QuotaCategoryVMFamilyMappingAPI) NewListBySubscriptionLocationResourcePager(
	_ string,
	_ *armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceOptions,
) *runtime.Pager[armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse] {
	pageNumber := 0
	return runtime.NewPager(runtime.PagingHandler[armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse]{
		More: func(page armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse) bool {
			return page.NextLink != nil
		},
		Fetcher: func(
			context.Context,
			*armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse,
		) (armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse, error) {
			v.calls.Add(1)
			if v.Error != nil && pageNumber == v.ErrorPage {
				return armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse{}, v.Error
			}

			pageSize := v.PageSize
			if pageSize <= 0 {
				pageSize = v.VMFamilies.Len()
			}
			start := pageNumber * pageSize
			end := min(start+pageSize, v.VMFamilies.Len())
			families := make([]*armcomputelimit.VMFamily, 0, end-start)
			for i := start; i < end; i++ {
				families = append(families, v.VMFamilies.Get(i))
			}
			pageNumber++

			var nextLink *string
			if end < v.VMFamilies.Len() {
				nextLink = new(string)
			}
			return armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse{
				VMFamilyListResult: armcomputelimit.VMFamilyListResult{
					Value:    families,
					NextLink: nextLink,
				},
			}, nil
		},
	})
}

func (v *QuotaCategoryVMFamilyMappingAPI) Calls() int64 {
	return v.calls.Load()
}

func (v *QuotaCategoryVMFamilyMappingAPI) Reset() {
	if v == nil {
		return
	}
	v.VMFamilies.Reset()
	v.Error = nil
	v.ErrorPage = 0
	v.PageSize = 0
	v.calls.Store(0)
}
