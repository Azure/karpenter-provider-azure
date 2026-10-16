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
	"context"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/samber/lo"
)

type NodeImageVersionsClient struct {
	client *armcontainerservice.Client
}

func NewNodeImageVersionsClient(subscriptionID string, cred azcore.TokenCredential, opts *arm.ClientOptions) (*NodeImageVersionsClient, error) {
	client, err := armcontainerservice.NewClient(subscriptionID, cred, opts)
	if err != nil {
		return nil, err
	}
	return &NodeImageVersionsClient{
		client: client,
	}, nil
}

func (l *NodeImageVersionsClient) List(ctx context.Context, location string) ([]*armcontainerservice.NodeImageVersion, error) {
	pager := l.client.NewListNodeImageVersionsPager(location, nil)

	var allVersions []*armcontainerservice.NodeImageVersion
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		allVersions = append(allVersions, page.Value...)
	}

	return FilteredNodeImages(allVersions), nil
}

// FilteredNodeImages filters on two conditions
// 1. The image is the preferred version for the given OS and SKU. A SecurityPatch image is
// preferred when present; otherwise the latest standard image is retained.
// 2. the image belongs to a supported gallery (AKS Ubuntu, Azure Linux, or AKS Windows)
func FilteredNodeImages(nodeImageVersions []*armcontainerservice.NodeImageVersion) []*armcontainerservice.NodeImageVersion {
	latestImages := make(map[string]*armcontainerservice.NodeImageVersion)

	for _, image := range nodeImageVersions {
		if image == nil {
			continue
		}
		os := lo.FromPtr(image.OS)
		sku := lo.FromPtr(image.SKU)
		version := lo.FromPtr(image.Version)

		// Skip the galleries that Karpenter does not support
		if os != AKSUbuntuGalleryName && os != AKSAzureLinuxGalleryName && os != AKSWindowsGalleryName {
			continue
		}

		key := os + "-" + sku

		currentLatest, exists := latestImages[key]
		if !exists || isPreferredVersion(version, lo.FromPtr(currentLatest.Version)) {
			latestImages[key] = image
		}
	}

	filteredImages := make([]*armcontainerservice.NodeImageVersion, 0, len(latestImages))
	for _, image := range latestImages {
		filteredImages = append(filteredImages, image)
	}

	return filteredImages
}

func isPreferredVersion(version1, version2 string) bool {
	base1, patch1 := SplitSecurityPatchVersion(version1)
	base2, patch2 := SplitSecurityPatchVersion(version2)
	if patch1 != "" && patch2 == "" {
		return true
	}
	if patch1 == "" && patch2 != "" {
		return false
	}
	if patch1 == "" {
		return isNewerVersion(base1, base2)
	}
	if comparison := compareVersionSegments(base1, base2); comparison != 0 {
		return comparison > 0
	}
	return compareVersionSegments(patch1, patch2) > 0
}

// SplitSecurityPatchVersion separates a composite SecurityPatch version into its standard base
// version and patch date. Standard versions are returned with an empty patch date.
func SplitSecurityPatchVersion(version string) (base, patchDate string) {
	base, patchDate, _ = strings.Cut(version, "-")
	return base, patchDate
}

// isNewerVersion will return if version1 is greater than version2, note the new versioning scheme is yearmm.dd.build, previously it was yy.mm.dd without the build id.
func isNewerVersion(version1, version2 string) bool {
	return compareVersionSegments(version1, version2) > 0
}

func compareVersionSegments(version1, version2 string) int {
	v1Segments := strings.Split(version1, ".")
	v2Segments := strings.Split(version2, ".")

	for i := 0; i < len(v1Segments) && i < len(v2Segments); i++ {
		v1Segment, err1 := strconv.Atoi(v1Segments[i])
		v2Segment, err2 := strconv.Atoi(v2Segments[i])

		if err1 != nil || err2 != nil {
			return 0
		}

		if v1Segment > v2Segment {
			return 1
		} else if v1Segment < v2Segment {
			return -1
		}
	}

	// If all segments are equal up to the length of the shorter version,
	// the longer version is considered newer if it has additional segments
	// the legacy linux versions use "yy.mm.dd" whereas new linux versions use "yymm.dd.build"
	return len(v1Segments) - len(v2Segments)
}
