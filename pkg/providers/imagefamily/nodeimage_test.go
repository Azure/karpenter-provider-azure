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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	customerSubscription = "12345678-1234-1234-1234-123456789012"
	sigSubscription      = "10945678-1234-1234-1234-123456789012"

	cigImageVersion      = "202505.27.0"
	laterCIGImageVersion = "202605.27.0"

	sigImageVersion = "202505.27.0"
)

func getExpectedTestCIGImages(imageFamily string, version string, kubernetesVersion string) []imagefamily.NodeImage {
	var images []imagefamily.DefaultImageOutput
	if imageFamily == v1beta1.Ubuntu2204ImageFamily {
		images = imagefamily.Ubuntu2204{}.DefaultImages()
	} else if imageFamily == v1beta1.AzureLinuxImageFamily {
		if imagefamily.UseAzureLinux3(kubernetesVersion) {
			images = imagefamily.AzureLinux3{}.DefaultImages()
		} else {
			images = imagefamily.AzureLinux{}.DefaultImages()
		}
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

//nolint:unparam // might always be using the same version in test, but could change in the future
func getExpectedTestSIGImages(imageFamily string, version string, kubernetesVersion string) []imagefamily.NodeImage {
	var images []imagefamily.DefaultImageOutput
	if imageFamily == v1beta1.Ubuntu2204ImageFamily {
		images = imagefamily.Ubuntu2204{}.DefaultImages()
	} else if imageFamily == v1beta1.AzureLinuxImageFamily {
		if imagefamily.UseAzureLinux3(kubernetesVersion) {
			images = imagefamily.AzureLinux3{}.DefaultImages()
		} else {
			images = imagefamily.AzureLinux{}.DefaultImages()
		}
	}
	nodeImages := []imagefamily.NodeImage{}
	for _, image := range images {
		nodeImages = append(nodeImages, imagefamily.NodeImage{
			ID:           fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s/versions/%s", sigSubscription, image.GalleryResourceGroup, image.GalleryName, image.ImageDefinition, version),
			Requirements: image.Requirements,
		})
	}
	return nodeImages
}

var _ = Describe("NodeImageProvider tests", func() {
	var (
		communityImageVersionsAPI *fake.CommunityGalleryImageVersionsAPI

		nodeImageProvider imagefamily.NodeImageProvider
		nodeClass         *v1beta1.AKSNodeClass
		kubernetesVersion string
	)

	BeforeEach(func() {
		ctx = coreoptions.ToContext(ctx, coretest.Options())
		ctx = options.ToContext(ctx, test.Options())

		communityImageVersionsAPI = &fake.CommunityGalleryImageVersionsAPI{}
		cigImageVersionTest := cigImageVersion
		communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &cigImageVersionTest})
		nodeImageVersionsAPI := &fake.NodeImageVersionsAPI{}
		kubernetesVersionCache := cache.New(azurecache.KubernetesVersionTTL, azurecache.DefaultCleanupInterval)
		nodeImageProvider = imagefamily.NewProvider(env.KubernetesInterface, kubernetesVersionCache, communityImageVersionsAPI, fake.Region, customerSubscription, nodeImageVersionsAPI)
		kubernetesVersion = lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()

		nodeClass = test.AKSNodeClass()
		test.ApplyDefaultStatus(nodeClass, env)
	})

	Context("List CIG Images", func() {
		It("should fail if KubernetesVersionReady is false", func() {
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionFalseForTesting", "testing false kubernetes version status")
			_, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(fmt.Errorf("NodeClass condition %s, is in Ready=%s, %s", v1beta1.ConditionTypeKubernetesVersionReady, "False", "testing false kubernetes version status")))
		})

		It("should match expected images for Ubuntu2204", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion, kubernetesVersion)))
		})

		// This test changes depending on the Kubernetes version, in effect making the following version-specific tests unnecessary.
		// They are still kept for clarity and to ensure that the behavior is explicitly tested.
		It("should match expected images for AzureLinux, depending on the Kubernetes version", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion, kubernetesVersion)))
		})

		It("should match expected images for AzureLinux with version < 1.32", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = "1.31.0"

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion, "1.31.0")))
		})

		It("should match expected images for AzureLinux with version >= 1.32", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = "1.32.0"

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion, "1.32.0")))
		})
	})

	Context("List SIG Images", func() {
		BeforeEach(func() {
			testOptions := options.FromContext(ctx)
			testOptions.UseSIG = true
			testOptions.SIGSubscriptionID = sigSubscription
			testOptions.SIGAccessTokenScope = "http://valid-scope.com/.default"
			testOptions.SIGAccessTokenServerURL = "http://valid-url.com"
			ctx = options.ToContext(ctx, testOptions)
		})

		It("should match expected images for Ubuntu2204", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion, kubernetesVersion)))
		})

		// This test changes depending on the Kubernetes version, in effect making the following version-specific tests unnecessary.
		// They are still kept for clarity and to ensure that the behavior is explicitly tested.
		It("should match expected images for AzureLinux, depending on the Kubernetes version", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion, kubernetesVersion)))
		})

		It("should match expected images for AzureLinux with version < 1.32", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = "1.31.0"

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion, "1.31.0")))
		})

		It("should match expected images for AzureLinux with version >= 1.32", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = "1.32.0"

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, sigImageVersion, "1.32.0")))
		})
	})

	Context("Caching tests", func() {
		It("should ensure List images uses cached data", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion, kubernetesVersion)))

			communityImageVersionsAPI.Reset()
			laterCIGImageVersionTest := laterCIGImageVersion
			communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &laterCIGImageVersionTest})

			foundImages, err = nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion, kubernetesVersion)))
		})

		It("should ensure List gets new image data if imageFamily changes", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, cigImageVersion, kubernetesVersion)))

			communityImageVersionsAPI.Reset()
			laterCIGImageVersionTest := laterCIGImageVersion
			communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &laterCIGImageVersionTest})

			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)

			foundImages, err = nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(foundImages).To(ContainElements(getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, laterCIGImageVersionTest, kubernetesVersion)))
		})
	})
})
