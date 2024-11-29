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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type NodeImageVersionsClient struct {
	cred azcore.TokenCredential
}

func NewNodeImageVersionsClient(cred azcore.TokenCredential) *NodeImageVersionsClient {
	return &NodeImageVersionsClient{
		cred: cred,
	}
}

func (l *NodeImageVersionsClient) List(ctx context.Context, location, subscription string) (NodeImageVersionsResponse, error) {
	resourceURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.ContainerService/locations/%s/nodeImageVersions?api-version=%s",
		subscription, location, "2024-04-02-preview",
	)

	token, err := l.cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", resourceURL, nil)
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}
	defer resp.Body.Close()

	var response NodeImageVersionsResponse
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&response)
	if err != nil {
		return NodeImageVersionsResponse{}, err
	}

	response.Values = FilteredNodeImages(response.Values)
	return response, nil
}

// FilteredNodeImages filters on two conditions
// 1. The image is the latest version for the given OS and SKU
// 2. the image belongs to a supported gallery(AKS Ubuntu or Azure Linux)
func FilteredNodeImages(nodeImageVersions []NodeImageVersion) []NodeImageVersion {
	latestImages := make(map[string]NodeImageVersion)

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

	var filteredImages []NodeImageVersion
	for _, image := range latestImages {
		filteredImages = append(filteredImages, image)
	}
	return filteredImages
}

func isNewerVersion(version1, version2 string) bool {
	// Assuming version is in the format: "year.month.day.build"
	// Split by dots and compare each segment as an integer

	var v1, v2 [4]int
	fmt.Sscanf(version1, "%d.%d.%d.%d", &v1[0], &v1[1], &v1[2], &v1[3]) //nolint:errcheck
	fmt.Sscanf(version2, "%d.%d.%d.%d", &v2[0], &v2[1], &v2[2], &v2[3]) //nolint:errcheck

	for i := 0; i < 4; i++ {
		if v1[i] > v2[i] {
			return true
		} else if v1[i] < v2[i] {
			return false
		}
	}

	return false
}
