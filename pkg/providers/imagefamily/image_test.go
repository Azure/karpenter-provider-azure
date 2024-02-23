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
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/samber/lo"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
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

var _ = BeforeSuite(func() {
	location := fake.Region

	defaultImageVersions := []*armcompute.CommunityGalleryImageVersion{
		{
			Name:     lo.ToPtr("1.1686127203.20215"),
			Location: &location,
			Type:     lo.ToPtr("Microsoft.Compute/galleries/images/versions"),
			Properties: &armcompute.CommunityGalleryImageVersionProperties{
				PublishedDate: lo.ToPtr(time.Now().Add(time.Minute * -10)),
			},
		},
		{
			Name:     lo.ToPtr("1.1686127203.20213"),
			Location: &location,
			Type:     lo.ToPtr("Microsoft.Compute/galleries/images/versions"),
			Properties: &armcompute.CommunityGalleryImageVersionProperties{
				PublishedDate: lo.ToPtr(time.Now().Add(time.Minute * -20)),
			},
		},
		{
			Name:     lo.ToPtr(latestImageVersion),
			Location: &location,
			Type:     lo.ToPtr("Microsoft.Compute/galleries/images/versions"),
			Properties: &armcompute.CommunityGalleryImageVersionProperties{
				PublishedDate: lo.ToPtr(time.Now().Add(time.Minute * -5)),
			},
		},
		{
			Name:     lo.ToPtr(olderImageVersion),
			Location: &location,
			Type:     lo.ToPtr("Microsoft.Compute/galleries/images/versions"),
			Properties: &armcompute.CommunityGalleryImageVersionProperties{
				PublishedDate: lo.ToPtr(time.Now().Add(time.Minute * -15)),
			},
		},
		{
			Name:     lo.ToPtr("1.1686127203.20216"),
			Location: &location,
			Type:     lo.ToPtr("Microsoft.Compute/galleries/images/versions"),
			Properties: &armcompute.CommunityGalleryImageVersionProperties{
				PublishedDate: lo.ToPtr(time.Now().Add(time.Minute * -7)),
			},
		},
	}

	versionsClient := &fake.CommunityGalleryImageVersionsAPI{}
	versionsClient.ImageVersions.Append(defaultImageVersions...)
	imageProvider = imagefamily.NewProvider(nil, nil, versionsClient, fake.Region)
})

func newTestNodeClass(imageID, imageVersion string) *v1alpha2.AKSNodeClass {
	nodeClass := test.AKSNodeClass()

	if imageID != "" {
		nodeClass.Spec.ImageID = lo.ToPtr(imageID)
	}
	if imageVersion != "" {
		nodeClass.Spec.ImageVersion = lo.ToPtr(imageVersion)
	}
	return nodeClass
}

var _ = Describe("Image ID Resolution", func() {
	var (
		nodeClassWithImageID           = newTestNodeClass(testImageID, "")
		nodeClassWithImageIDAndVersion = newTestNodeClass(testImageID, olderImageVersion)
		nodeClassWithImageVersion      = newTestNodeClass("", olderImageVersion)
	)

	DescribeTable("Resolution Of Image ID",
		func(nodeClass *v1alpha2.AKSNodeClass, instanceType *cloudprovider.InstanceType, imageFamily interface{}, expectedImageID string) {
			imageID, err := imageProvider.Get(context.Background(), nodeClass, instanceType, imagefamily.Ubuntu2204{})
			Expect(imageID).To(Equal(expectedImageID))
			Expect(err).To(BeNil())
		},
		Entry("Image ID is specified in the NodeClass", nodeClassWithImageID, &cloudprovider.InstanceType{}, imagefamily.Ubuntu2204{}, testImageID),
		Entry("Image ID and ImageVersion are specified in the NodeClass", nodeClassWithImageIDAndVersion, &cloudprovider.InstanceType{}, imagefamily.Ubuntu2204{}, testImageID),
		Entry("ImageVersion is specified in the NodeClass", nodeClassWithImageVersion, &cloudprovider.InstanceType{}, imagefamily.Ubuntu2204{}, fmt.Sprintf("/CommunityGalleries/%s/images/%s/versions/%s", imagefamily.AKSUbuntuPublicGalleryURL, imagefamily.Ubuntu2204Gen2CommunityImage, olderImageVersion)),
	)

	DescribeTable("Resolution Of Image ID",
		func(communityImageName, publicGalleryURL, versionName string, expectedImageID string) {
			imageID, err := imageProvider.GetImageID(communityImageName, publicGalleryURL, versionName)
			Expect(imageID).To(Equal(expectedImageID))
			Expect(err).To(BeNil())
		},
		Entry("Image version is empty, should get latest", imagefamily.Ubuntu2204Gen2CommunityImage, imagefamily.AKSUbuntuPublicGalleryURL, "", fmt.Sprintf("/CommunityGalleries/%s/images/%s/versions/%s", imagefamily.AKSUbuntuPublicGalleryURL, imagefamily.Ubuntu2204Gen2CommunityImage, latestImageVersion)),
		Entry("Image version is specified, should use it", imagefamily.Ubuntu2204Gen2CommunityImage, imagefamily.AKSUbuntuPublicGalleryURL, olderImageVersion, fmt.Sprintf("/CommunityGalleries/%s/images/%s/versions/%s", imagefamily.AKSUbuntuPublicGalleryURL, imagefamily.Ubuntu2204Gen2CommunityImage, olderImageVersion)),
	)

})

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
