// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package imagefamily

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	armcomputev5 "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/aws/karpenter-core/pkg/scheduling"
)

const (
	AKSUbuntuPublicGalleryURL = "AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2"
)

// DefaultImageOutput is the Stub of an Image we return from an ImageFamily De
type DefaultImageOutput struct {
	CommunityImage   string
	PublicGalleryURL string
	Requirements     scheduling.Requirements
}

// CommunityGalleryImageVersionsAPI is used for listing community gallery image versions.
type CommunityGalleryImageVersionsAPI interface {
	NewListPager(location string, publicGalleryName string, galleryImageName string, options *armcomputev5.CommunityGalleryImageVersionsClientListOptions) *runtime.Pager[armcomputev5.CommunityGalleryImageVersionsClientListResponse]
}
