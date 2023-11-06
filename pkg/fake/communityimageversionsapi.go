// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fake

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/Azure/karpenter/pkg/providers/imagefamily"
)

type CommunityGalleryImageVersionsAPI struct {
	ImageVersions AtomicPtrSlice[armcompute.CommunityGalleryImageVersion]
}

// assert that the fake implements the interface
var _ imagefamily.CommunityGalleryImageVersionsAPI = (*CommunityGalleryImageVersionsAPI)(nil)

// NewListPager returns a new pager to return the next page of CommunityGalleryImageVersionsClientListResponse
func (c *CommunityGalleryImageVersionsAPI) NewListPager(_ string, _ string, _ string, _ *armcompute.CommunityGalleryImageVersionsClientListOptions) *runtime.Pager[armcompute.CommunityGalleryImageVersionsClientListResponse] {
	pagingHandler := runtime.PagingHandler[armcompute.CommunityGalleryImageVersionsClientListResponse]{
		More: func(page armcompute.CommunityGalleryImageVersionsClientListResponse) bool {
			return false
		},
		Fetcher: func(ctx context.Context, _ *armcompute.CommunityGalleryImageVersionsClientListResponse) (armcompute.CommunityGalleryImageVersionsClientListResponse, error) {
			output := armcompute.CommunityGalleryImageVersionList{
				Value: []*armcompute.CommunityGalleryImageVersion{},
			}
			output.Value = append(output.Value, c.ImageVersions.values...)
			return armcompute.CommunityGalleryImageVersionsClientListResponse{
				CommunityGalleryImageVersionList: output,
			}, nil
		},
	}
	return runtime.NewPager(pagingHandler)
}

func (c *CommunityGalleryImageVersionsAPI) Reset() {
	if c == nil {
		return
	}
	c.ImageVersions.Reset()
}
