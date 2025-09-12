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
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	imagefamilytypes "github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/types"
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

func getExpectedTestCIGImages(imageFamily string, fipsMode *v1beta1.FIPSMode, version string, kubernetesVersion string) []imagefamily.NodeImage {
	var images []imagefamilytypes.DefaultImageOutput
	if imageFamily == v1beta1.Ubuntu2204ImageFamily {
		images = imagefamily.Ubuntu2204{}.DefaultImages(false, fipsMode)
	} else if imageFamily == v1beta1.AzureLinuxImageFamily {
		if imagefamily.UseAzureLinux3(kubernetesVersion) {
			images = imagefamily.AzureLinux3{}.DefaultImages(false, fipsMode)
		} else {
			images = imagefamily.AzureLinux{}.DefaultImages(false, fipsMode)
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
func getExpectedTestSIGImages(imageFamily string, fipsMode *v1beta1.FIPSMode, version string, kubernetesVersion string) []imagefamily.NodeImage {
	var images []imagefamilytypes.DefaultImageOutput
	var actualImageFamily imagefamily.ImageFamily
	if imageFamily == v1beta1.UbuntuImageFamily {
		if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS {
			actualImageFamily = &imagefamily.Ubuntu2004{}
		} else {
			actualImageFamily = &imagefamily.Ubuntu2204{}
		}
	} else if imageFamily == v1beta1.Ubuntu2204ImageFamily {
		actualImageFamily = &imagefamily.Ubuntu2204{}
	} else if imageFamily == v1beta1.AzureLinuxImageFamily {
		if imagefamily.UseAzureLinux3(kubernetesVersion) {
			actualImageFamily = &imagefamily.AzureLinux3{}
		} else {
			actualImageFamily = &imagefamily.AzureLinux{}
		}
	}
	images = actualImageFamily.DefaultImages(true, fipsMode)
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
		testOptions               *options.Options
		communityImageVersionsAPI *fake.CommunityGalleryImageVersionsAPI

		nodeImageProvider imagefamily.NodeImageProvider
		nodeClass         *v1beta1.AKSNodeClass
		kubernetesVersion string
	)

	BeforeEach(func() {
		ctx = coreoptions.ToContext(ctx, coretest.Options())
		testOptions = test.Options()
		ctx = options.ToContext(ctx, testOptions)

		communityImageVersionsAPI = &fake.CommunityGalleryImageVersionsAPI{}
		cigImageVersionTest := cigImageVersion
		communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &cigImageVersionTest})
		nodeImageVersionsAPI := &fake.NodeImageVersionsAPI{}
		nodeImageProvider = imagefamily.NewProvider(communityImageVersionsAPI, fake.Region, customerSubscription, nodeImageVersionsAPI, cache.New(imagefamily.ImageExpirationInterval, imagefamily.ImageCacheCleaningInterval))
		kubernetesVersion = lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()

		nodeClass = test.AKSNodeClass()
		test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
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
			expectedImages := getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, kubernetesVersion)
			Expect(foundImages).To(Equal(expectedImages))
		})

		// This test changes depending on the Kubernetes version, in effect making the following version-specific tests unnecessary.
		// They are still kept for clarity and to ensure that the behavior is explicitly tested.
		It("should match expected images for AzureLinux, depending on the Kubernetes version", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			azLinuxImages := getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, kubernetesVersion)
			Expect(foundImages).To(Equal(azLinuxImages))
		})

		It("should match expected images for AzureLinux with version < 1.32", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = "1.31.0"

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			azLinuxV2Images := getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, "1.31.0")
			Expect(foundImages).To(Equal(azLinuxV2Images))
		})

		It("should match expected images for AzureLinux with version >= 1.32", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Status.KubernetesVersion = "1.32.0"

			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			azLinuxV3Images := getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, "1.32.0")
			Expect(foundImages).To(Equal(azLinuxV3Images))

			// Explicitly verify ARM64 image is NOT included in CIG (Community Image Gallery)
			Expect(foundImages).ToNot(ContainElement(HaveField("ID", ContainSubstring("V3gen2arm64"))))
		})
	})

	Context("List SIG Images", func() {
		BeforeEach(func() {
			testOptions = options.FromContext(ctx)
			testOptions.UseSIG = true
			testOptions.SIGSubscriptionID = sigSubscription
			testOptions.SIGAccessTokenServerURL = "http://valid-url.com"
			ctx = options.ToContext(ctx, testOptions)
		})

		Context("List FIPS Images When FIPSMode Is Explicitly FIPS", func() {
			BeforeEach(func() {
				nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
			})

			It("should match expected images for FIPS when using generic Ubuntu", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})

			//TODO: Modify when Ubuntu 22.04 with FIPS becomes available
			It("should match expected images for FIPS Ubuntu2204", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)

				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundImages).To(BeEmpty())
			})

			// This test changes depending on the Kubernetes version, in effect making version-specific tests unnecessary.
			// They are still kept for clarity and to ensure that the behavior is explicitly tested.
			It("should match expected images for FIPS AzureLinux, depending on the Kubernetes version", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})

		})

		// current behavior for not setting FIPSMode is effectively setting it to Disabled
		Context("List Default Images When FIPSMode Is Not Explicitly Set", func() {
			BeforeEach(func() {
				nodeClass.Spec.FIPSMode = nil
			})

			It("should match expected images for generic Ubuntu", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})

			It("should match expected images for Ubuntu2204", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})

			// This test changes depending on the Kubernetes version, in effect making version-specific tests unnecessary.
			// They are still kept for clarity and to ensure that the behavior is explicitly tested.
			It("should match expected images for default AzureLinux, depending on the Kubernetes version", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})

		})

		Context("List Default Images When FIPSMode Is Explicitly Disabled", func() {
			BeforeEach(func() {
				nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeDisabled
			})

			It("should match expected images for generic Ubuntu", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))

			})

			It("should match expected images for Ubuntu2204", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})

			// This test changes depending on the Kubernetes version, in effect making version-specific tests unnecessary.
			// They are still kept for clarity and to ensure that the behavior is explicitly tested.
			It("should match expected images for default AzureLinux, depending on the Kubernetes version", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))
			})
		})

		DescribeTable("should match expected images",
			func(imageFamily *string, fipsMode *v1beta1.FIPSMode, version string, kubernetesVersion string) {
				nodeClass.Spec.ImageFamily = imageFamily
				nodeClass.Spec.FIPSMode = fipsMode
				nodeClass.Status.KubernetesVersion = kubernetesVersion

				foundImages, err := nodeImageProvider.List(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				expectedImages := getExpectedTestSIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, sigImageVersion, kubernetesVersion)
				Expect(foundImages).To(Equal(expectedImages))

				if imagefamily.UseAzureLinux3(kubernetesVersion) && lo.FromPtr(nodeClass.Spec.FIPSMode) != v1beta1.FIPSModeFIPS {
					// Explicitly verify ARM64 image IS included in SIG (Shared Image Gallery)
					Expect(foundImages).To(ContainElement(And(
						HaveField("ID", ContainSubstring("V3gen2arm64")),
						Not(HaveField("ID", ContainSubstring("V3gen2arm64fips"))),
					)))
				}
			},
			Entry("for default AzureLinux with version < 1.32 when FIPSMode is explicitly set to Disabled", lo.ToPtr(v1beta1.AzureLinuxImageFamily), &v1beta1.FIPSModeDisabled, sigImageVersion, "1.31.0"),
			Entry("for default AzureLinux with version < 1.32 when FIPSMode is not explicitly set", lo.ToPtr(v1beta1.AzureLinuxImageFamily), nil, sigImageVersion, "1.31.0"),
			Entry("for FIPS AzureLinux with version < 1.32 when FIPSMode is explicitly set to FIPS", lo.ToPtr(v1beta1.AzureLinuxImageFamily), &v1beta1.FIPSModeFIPS, sigImageVersion, "1.31.0"),
			Entry("for default AzureLinux with version >= 1.32 when FIPSMode is explicitly set to Disabled", lo.ToPtr(v1beta1.AzureLinuxImageFamily), &v1beta1.FIPSModeDisabled, sigImageVersion, "1.32.0"),
			Entry("for default AzureLinux with version >= 1.32 when FIPSMode is not explicitly set", lo.ToPtr(v1beta1.AzureLinuxImageFamily), nil, sigImageVersion, "1.32.0"),
			Entry("for FIPS AzureLinux with version >= 1.32 when FIPSMode is explicitly set to FIPS", lo.ToPtr(v1beta1.AzureLinuxImageFamily), &v1beta1.FIPSModeFIPS, sigImageVersion, "1.32.0"),
		)
	})

	Context("Caching tests", func() {
		It("should ensure List images uses cached data", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			expectedImages := getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, kubernetesVersion)
			Expect(foundImages).To(Equal(expectedImages))

			communityImageVersionsAPI.Reset()
			laterCIGImageVersionTest := laterCIGImageVersion
			communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &laterCIGImageVersionTest})

			foundImages, err = nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			expectedImages = getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, kubernetesVersion)
			Expect(foundImages).To(Equal(expectedImages))
		})

		It("should ensure List gets new image data if imageFamily changes", func() {
			foundImages, err := nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			expectedImages := getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, cigImageVersion, kubernetesVersion)
			Expect(foundImages).To(Equal(expectedImages))

			communityImageVersionsAPI.Reset()
			laterCIGImageVersionTest := laterCIGImageVersion
			communityImageVersionsAPI.ImageVersions.Append(&armcompute.CommunityGalleryImageVersion{Name: &laterCIGImageVersionTest})

			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)

			foundImages, err = nodeImageProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			expectedImages = getExpectedTestCIGImages(*nodeClass.Spec.ImageFamily, nodeClass.Spec.FIPSMode, laterCIGImageVersionTest, kubernetesVersion)
			Expect(foundImages).To(Equal(expectedImages))
		})
	})
})
