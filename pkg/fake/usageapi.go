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

	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
)

type UsageAPI struct {
	Usages AtomicPtrSlice[armcompute.Usage]
	Error  error
}

// assert that the fake implements the interface
var _ quota.UsageAPI = &UsageAPI{}

func (u *UsageAPI) NewListPager(_ string, _ *armcompute.UsageClientListOptions) *runtime.Pager[armcompute.UsageClientListResponse] {
	pagingHandler := runtime.PagingHandler[armcompute.UsageClientListResponse]{
		More: func(page armcompute.UsageClientListResponse) bool {
			return false
		},
		Fetcher: func(ctx context.Context, _ *armcompute.UsageClientListResponse) (armcompute.UsageClientListResponse, error) {
			if u.Error != nil {
				return armcompute.UsageClientListResponse{}, u.Error
			}
			output := armcompute.ListUsagesResult{
				Value: []*armcompute.Usage{},
			}
			output.Value = append(output.Value, u.Usages.values...)
			return armcompute.UsageClientListResponse{
				ListUsagesResult: output,
			}, nil
		},
	}
	return runtime.NewPager(pagingHandler)
}

func (u *UsageAPI) Reset() {
	if u == nil {
		return
	}
	u.Usages.Reset()
	u.Error = nil
}
