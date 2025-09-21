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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	types "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
)

type NodeImageVersionsClient struct {
	cred  azcore.TokenCredential
	cloud cloud.Configuration
}

func NewNodeImageVersionsClient(cred azcore.TokenCredential, cloud cloud.Configuration) *NodeImageVersionsClient {
	return &NodeImageVersionsClient{
		cred:  cred,
		cloud: cloud,
	}
}

func (l *NodeImageVersionsClient) List(ctx context.Context, location, subscription string) (types.NodeImageVersionsResponse, error) {
	resourceManagerConfig := l.cloud.Services[cloud.ResourceManager]

	resourceURL := fmt.Sprintf(
		"%s/subscriptions/%s/providers/Microsoft.ContainerService/locations/%s/nodeImageVersions?api-version=%s",
		resourceManagerConfig.Endpoint, subscription, location, "2024-04-02-preview",
	)

	token, err := l.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{auth.TokenScope(l.cloud)},
	})
	if err != nil {
		return types.NodeImageVersionsResponse{}, err
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", resourceURL, nil)
	if err != nil {
		return types.NodeImageVersionsResponse{}, err
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return types.NodeImageVersionsResponse{}, err
	}
	defer resp.Body.Close()

	var response types.NodeImageVersionsResponse
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&response)
	if err != nil {
		return types.NodeImageVersionsResponse{}, err
	}

	response.Values = FilteredNodeImages(response.Values)
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
