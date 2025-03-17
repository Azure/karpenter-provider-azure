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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/patrickmn/go-cache"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
)

const (
	subscription = "12345678-1234-1234-1234-123456789012"

	cigImageVersion = "202410.09.0"

	sigImageVersion = "202410.09.0"
)

func getExpectedTestCIGImages(imageFamily string, version string) []imagefamily.NodeImage {
	var images []imagefamily.DefaultImageOutput
	if imageFamily == v1alpha2.Ubuntu2204ImageFamily {
		images = imagefamily.Ubuntu2204{}.DefaultImages()
	} else if imageFamily == v1alpha2.AzureLinuxImageFamily {
		images = imagefamily.AzureLinux{}.DefaultImages()
	}
	nodeImages := []imagefamily.NodeImage{}
	for _, image := range images {
		nodeImages = append(nodeImages, imagefamily.NodeImage{
			ID:           fmt.Sprintf("/CommunityGalleries/%s/images/%s/versions/%s", image.PublicGalleryURL, image.ImageDefinition, version),
			Requirements: image.Requirements,
		})
	}
	return nodeImages
}

func getExpectedTestSIGImages(imageFamily string, version string) []imagefamily.NodeImage {
	var images []imagefamily.DefaultImageOutput
	if imageFamily == v1alpha2.Ubuntu2204ImageFamily {
		images = imagefamily.Ubuntu2204{}.DefaultImages()
	} else if imageFamily == v1alpha2.AzureLinuxImageFamily {
		images = imagefamily.AzureLinux{}.DefaultImages()
	}
	nodeImages := []imagefamily.NodeImage{}
	for _, image := range images {
		nodeImages = append(nodeImages, imagefamily.NodeImage{
			ID:           fmt.Sprintf("/subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s", image.GalleryResourceGroup, image.GalleryName, image.ImageDefinition, version),
			Requirements: image.Requirements,
		})
	}
	return nodeImages
}

var _ = Describe("NodeImageProvider tests", func() {
	var (
		env *coretest.Environment

		nodeImageProvider imagefamily.NodeImageProvider
		nodeClass         *v1alpha2.AKSNodeClass
	)

	BeforeEach(func() {
		env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...), coretest.WithFieldIndexers(test.AKSNodeClassFieldIndexer(ctx)))
		ctx = coreoptions.ToContext(ctx, coretest.Options())
		ctx = options.ToContext(ctx, test.Options())

		communityImageVersionsAPI := &fake.CommunityGalleryImageVersionsAPI{}
		var cigImageVersionTest = cigImageVersion
		communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &cigImageVersionTest})
		nodeImageVersionsAPI := &fake.NodeImageVersionsAPI{}
		kubernetesVersionCache := cache.New(azurecache.KubernetesVersionTTL, azurecache.DefaultCleanupInterval)
		nodeImageProvider = imagefamily.NewProvider(env.KubernetesInterface, kubernetesVersionCache, communityImageVersionsAPI, fake.Region, subscription, nodeImageVersionsAPI)

		nodeClass = test.AKSNodeClass()
	})

	Context("CIG Images", func() {
		It("Ubuntu2204 successful list case", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion)))
		})

		It("AzureLinux successful list case", func() {
			var imageFamily = v1alpha2.AzureLinuxImageFamily
			nodeClass.Spec.ImageFamily = &imageFamily

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion)))
		})
	})

	Context("SIG Images", func() {
		BeforeEach(func() {
			var varTrue = true
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{UseSIG: &varTrue}))
		})

		It("Ubuntu2204 successful list case", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion)))
		})

		It("AzureLinux successful list case", func() {
			var imageFamily = v1alpha2.AzureLinuxImageFamily
			nodeClass.Spec.ImageFamily = &imageFamily

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion)))
		})
	})
})
