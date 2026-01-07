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
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/stretchr/testify/assert"
)

func TestFilteredNodeImagesGalleryFilter(t *testing.T) {
	nodeImageVersionAPI := NodeImageVersionsAPI{}
	nodeImageVersions, _ := nodeImageVersionAPI.List(context.TODO(), "")
	filteredNodeImages := imagefamily.FilteredNodeImages(nodeImageVersions)
	for _, val := range filteredNodeImages {
		assert.NotEqual(t, *val.OS, "AKSWindows")
		assert.NotEqual(t, *val.OS, "AKSUbuntuEdgeZone")
	}
}

// The reasoning behind the test is the following set of output
// az rest --method get --url "/subscriptions/<redacted>/providers/Microsoft.ContainerService/locations/westus2/nodeImageVersions?api-version=2024-04-02-preview" | jq '.values[] | select(.sku == "2204gen2containerd")'
//
//	{
//	"fullName": "AKSUbuntuEdgeZone-2204gen2containerd-202411.12.0",
//	"os": "AKSUbuntuEdgeZone",
//	"sku": "2204gen2containerd",
//	"version": "202411.12.0"
//	}
//	{
//	"fullName": "AKSUbuntu-2204gen2containerd-2022.10.03",
//	"os": "AKSUbuntu",
//	"sku": "2204gen2containerd",
//	"version": "2022.10.03"
//	}
//	{
//	"fullName": "AKSUbuntu-2204gen2containerd-202411.12.0",
//	"os": "AKSUbuntu",
//	"sku": "2204gen2containerd",
//	"version": "202411.12.0"
//	}
//
// In some cases, due to a different distro implementation of the same os + sku pairing, we can get
// duplicate entries for os + sku matchings.
// This test validates we simply ignore the legacy distros and take in the latest Version.
func TestFilteredNodeImagesMinimalUbuntuEdgeCase(t *testing.T) {
	filteredNodeImages := imagefamily.FilteredNodeImages(nodeImageVersions)

	expectedVersion := "202505.27.0"
	found := false

	for _, val := range filteredNodeImages {
		if *val.SKU == "2204gen2containerd" && *val.Version == expectedVersion {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find node image with version %s but it was not present in the filtered results", expectedVersion)
	}
}

// This function tests that the node image versions that come from the fake are already filtered with the right values
// similar to the behavior we would see if someone is using the node image versions api call.
// the fake imports the same clientside filtering so we need to assert that behavior is the same
func TestFilteredNodeImageVersionsFromProviderList(t *testing.T) {
	nodeImageVersionsAPI := NodeImageVersionsAPI{}
	filteredNodeImages, err := nodeImageVersionsAPI.List(context.TODO(), "")
	assert.Nil(t, err)

	expectedVersion := "202505.27.0"
	found := false

	for _, val := range filteredNodeImages {
		if *val.SKU == "2204gen2containerd" && *val.Version == expectedVersion {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find node image with version %s but it was not present in the filtered results", expectedVersion)
	}
}
