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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
)

type CommunityGalleryImageVersionsAPI struct {
	ImageVersions AtomicPtrSlice[armcompute.CommunityGalleryImageVersion]
}

// assert that the fake implements the interface
var _ imagefamily.CommunityGalleryImageVersionsAPI = &CommunityGalleryImageVersionsAPI{}

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

func (c *CommunityGalleryImageVersionsAPI) Get(_ context.Context, location string, publicGalleryName string, galleryImageName string, galleryImageVersionName string, options *armcompute.CommunityGalleryImageVersionsClientGetOptions) (armcompute.CommunityGalleryImageVersionsClientGetResponse, error) {
	// TODO: Add case where this get doesn't work or succeed
	return armcompute.CommunityGalleryImageVersionsClientGetResponse{
		CommunityGalleryImageVersion: armcompute.CommunityGalleryImageVersion{
			Identifier: &armcompute.CommunityGalleryIdentifier{
				UniqueID: lo.ToPtr(fmt.Sprintf("/CommunityGalleries/%s/images/%s/versions/%s", publicGalleryName, galleryImageName, galleryImageVersionName)),
			},
		},
	}, nil
}

func (c *CommunityGalleryImageVersionsAPI) Reset() {
	if c == nil {
		return
	}
	c.ImageVersions.Reset()
}
