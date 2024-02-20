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

package imagefamily_test

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	
	"github.com/samber/lo"
)

var imageProvider *imagefamily.Provider

func TestAzure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Providers/ImageProvider/Azure")
}

const (
	testImageID        = "/CommunityGalleries/previewaks-1a06572d-8508-419c-a0d1-baffcbcb2f3b/images/2204gen2containerd/Versions/1.1685741267.25933"
	olderImageVersion  = "1.1686127203.20214"
	latestImageVersion = "1.1686127203.20217"
)

var _ = Describe("Image ID Parsing", func() {
	DescribeTable("Parse Image ID",
		func(imageID string, expectedPublicGalleryURL, expectedCommunityImageName, expectedImageVersion string, expectError bool) {
			publicGalleryURL, communityImageName, imageVersion, err := imagefamily.ParseCommunityImageIDInfo(imageID)
			if expectError {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).To(BeNil())
			Expect(publicGalleryURL).To(Equal(expectedPublicGalleryURL))
			Expect(communityImageName).To(Equal(expectedCommunityImageName))
			Expect(imageVersion).To(Equal(expectedImageVersion))
		},
		Entry("Valid image id should parse", fmt.Sprintf("/CommunityGalleries/%s/images/%s/versions/%s", imagefamily.AKSUbuntuPublicGalleryURL, imagefamily.Ubuntu2204Gen2CommunityImage, olderImageVersion), imagefamily.AKSUbuntuPublicGalleryURL, imagefamily.Ubuntu2204Gen2CommunityImage, olderImageVersion, nil),
		Entry("invalid image id should not parse", "badimageid", "", "", "", true),
		Entry("empty image id should not parse", "badimageid", "", "", "", true),
	)
})
