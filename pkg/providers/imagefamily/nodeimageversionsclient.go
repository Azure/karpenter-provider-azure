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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
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

func (l *NodeImageVersionsClient) List(ctx context.Context, location, subscription string) (types.NodeImageVersionsResponse, error) {
	pager := l.client.NewListNodeImageVersionsPager(location, nil)
	
	var allVersions []types.NodeImageVersion
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return types.NodeImageVersionsResponse{}, err
		}
		
		// Convert SDK types to our internal types
		for _, sdkVersion := range page.Value {
			if sdkVersion == nil {
				continue
			}
			nodeImageVersion := types.NodeImageVersion{
				FullName: lo.FromPtr(sdkVersion.FullName),
				OS:       lo.FromPtr(sdkVersion.OS),
				SKU:      lo.FromPtr(sdkVersion.SKU),
				Version:  lo.FromPtr(sdkVersion.Version),
			}
			allVersions = append(allVersions, nodeImageVersion)
		}
	}

	response := types.NodeImageVersionsResponse{
		Values: FilteredNodeImages(allVersions),
	}
	return response, nil
}

// FilteredNodeImages filters on two conditions
// 1. The image is the latest version for the given OS and SKU
// 2. the image belongs to a supported gallery(AKS Ubuntu or Azure Linux)
func FilteredNodeImages(nodeImageVersions []types.NodeImageVersion) []types.NodeImageVersion {
	latestImages := make(map[string]types.NodeImageVersion)

	for _, image := range nodeImageVersions {
		// Skip the galleries that Karpenter does not support
		if image.OS != AKSUbuntuGalleryName && image.OS != AKSAzureLinuxGalleryName {
			continue
		}

		key := image.OS + "-" + image.SKU

		currentLatest, exists := latestImages[key]
		if !exists || isNewerVersion(image.Version, currentLatest.Version) {
			latestImages[key] = image
		}
	}

	var filteredImages []types.NodeImageVersion
	for _, image := range latestImages {
		filteredImages = append(filteredImages, image)
	}
	return filteredImages
}

// isNewerVersion will return if version1 is greater than version2, note the new versioning scheme is yearmm.dd.build, previously it was yy.mm.dd without the build id.
func isNewerVersion(version1, version2 string) bool {
	// Split by dots and compare each segment as an integer getting the largest vhd version
	v1Segments := strings.Split(version1, ".")
	v2Segments := strings.Split(version2, ".")

	for i := 0; i < len(v1Segments) && i < len(v2Segments); i++ {
		v1Segment, err1 := strconv.Atoi(v1Segments[i])
		v2Segment, err2 := strconv.Atoi(v2Segments[i])

		if err1 != nil || err2 != nil {
			return false
		}

		if v1Segment > v2Segment {
			return true
		} else if v1Segment < v2Segment {
			return false
		}
	}

	// If all segments are equal up to the length of the shorter version,
	// the longer version is considered newer if it has additional segments
	// the legacy linux versions use "yy.mm.dd" whereas new linux versions use "yymm.dd.build"
	return len(v1Segments) > len(v2Segments)
}
