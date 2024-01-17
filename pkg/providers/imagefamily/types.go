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

package imagefamily

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	armcomputev5 "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/aws/karpenter-core/pkg/scheduling"
)

const (
	AKSUbuntuPublicGalleryURL     = "AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2"
	AKSAzureLinuxPublicGalleryURL = "AKSAzureLinux-f7c7cda5-1c9a-4bdc-a222-9614c968580b"
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
