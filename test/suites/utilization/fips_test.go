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

package utilization_test

import (
	"fmt"
	"strings"

	opstatus "github.com/awslabs/operatorpkg/status"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

var _ = Describe("FIPS", Label("runner"), func() {
	Context("FIPS Validation", func() {
		It("should reject FIPS without SIG access", func() {
			if !env.InClusterController {
				Skip("Testing FIPS usage cleanly fails without SIG access only makes sense in self-hosted mode - NAP has SIG access")
			}

			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)

			env.ExpectCreated(nodeClass, nodePool)

			// Should fail to reconcile images due to SIG requirement
			Eventually(func(g Gomega) {
				g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady)
				g.Expect(condition.IsFalse()).To(BeTrue())
				g.Expect(condition.Reason).To(Equal("SIGRequiredForFIPS"))
				g.Expect(condition.Message).To(ContainSubstring("FIPS images require UseSIG to be enabled"))

				// Check that the overall Ready condition is also false
				readyCondition := nodeClass.StatusConditions().Get(opstatus.ConditionReady)
				g.Expect(readyCondition.IsFalse()).To(BeTrue())
			}).Should(Succeed())
		})
	})

	Context("FIPS Provisioning", func() {
		BeforeEach(func() {
			if env.InClusterController {
				Skip("FIPS tests require SIG access - skipping in self-hosted mode")
			}
		})

		It("should provision FIPS-enabled Ubuntu nodes", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.UbuntuImageFamily)
			nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			node := env.ExpectCreatedNodeCount("==", 1)[0]

			imageRef := expectNodeUsesFIPS(node)
			expectNodeClassHasExpectedImages(nodeClass, imageRef, true) // true = expect FIPS images
		})

		It("should provision FIPS-enabled AzureLinux nodes", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeFIPS)

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			node := env.ExpectCreatedNodeCount("==", 1)[0]

			imageRef := expectNodeUsesFIPS(node)
			expectNodeClassHasExpectedImages(nodeClass, imageRef, true) // true = expect FIPS images
		})
	})

	Context("FIPS Disabled", func() {
		It("should provision nodes with FIPS explicitly disabled", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			// Explicitly testing Disabled here.
			// - This should have the same behavior as not being set, which other tests will already cover.
			// However, since we don't have the defaulting actually set "Disabled" on the NodeClass, and instead
			// leaves it unset, this tests is run to ensure "Disabled" behavior is expected.
			// - Additionally, the default (unset behavior) may change in the future depending upon other settings.
			nodeClass.Spec.FIPSMode = lo.ToPtr(v1beta1.FIPSModeDisabled)

			pod := test.Pod()
			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			node := env.ExpectCreatedNodeCount("==", 1)[0]

			imageRef := expectNodeDoesNotUseFIPS(node)
			expectNodeClassHasExpectedImages(nodeClass, imageRef, false) // false = expect non-FIPS images
		})
	})
})

// expectNodeUsesFIPS checks that the node is using a FIPS-compliant image and returns the image reference
func expectNodeUsesFIPS(node *v1.Node) string {
	GinkgoHelper()

	// Extract VM name from the node's providerID
	vmName, err := nodeclaim.GetVMName(node.Spec.ProviderID)
	Expect(err).ToNot(HaveOccurred(), "Should be able to extract VM name from providerID")

	// Get the VM using the extracted name
	vm := env.GetVMByName(vmName)
	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())

	// Extract the image reference
	imageReference := utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference)
	Expect(imageReference).ToNot(BeEmpty(), "VM should have an image reference")

	// FIPS images require SIG (Shared Image Gallery) access
	// SIG images have ID field populated, while CIG (Community Gallery)
	// images use CommunityGalleryImageID
	isSIGImage := lo.FromPtr(vm.Properties.StorageProfile.ImageReference.ID) != ""

	// Verify that FIPS images are using SIG (as required)
	Expect(isSIGImage).To(BeTrue(),
		"FIPS images must use SIG (Shared Image Gallery), but image reference suggests CIG: %s", imageReference)

	// Check if this is a FIPS image by looking for "fips" in the image reference
	lowerImageRef := strings.ToLower(imageReference)
	Expect(lowerImageRef).To(ContainSubstring("fips"),
		"Expected FIPS image reference to contain 'fips': %s", imageReference)

	return imageReference
}

// expectNodeDoesNotUseFIPS checks that the node is using a non-FIPS image and returns the image reference
func expectNodeDoesNotUseFIPS(node *v1.Node) string {
	GinkgoHelper()

	// Extract VM name from the node's providerID
	vmName, err := nodeclaim.GetVMName(node.Spec.ProviderID)
	Expect(err).ToNot(HaveOccurred(), "Should be able to extract VM name from providerID")

	// Get the VM using the extracted name
	vm := env.GetVMByName(vmName)
	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile).ToNot(BeNil())
	Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())

	// Extract the image reference
	imageReference := utils.ImageReferenceToString(vm.Properties.StorageProfile.ImageReference)
	Expect(imageReference).ToNot(BeEmpty(), "VM should have an image reference")

	// Check that this is NOT a FIPS image
	lowerImageRef := strings.ToLower(imageReference)
	Expect(lowerImageRef).ToNot(ContainSubstring("fips"),
		"Expected non-FIPS image reference but got FIPS image: %s", imageReference)

	return imageReference
}

// expectNodeClassHasExpectedImages ensures that the given image reference exists in the NodeClass's resolved images
// and that all images in the NodeClass match the expected FIPS status
func expectNodeClassHasExpectedImages(nodeClass *v1beta1.AKSNodeClass, imageReference string, expectFIPS bool) {
	GinkgoHelper()

	// Ensure NodeClass has been properly reconciled with available images
	Eventually(func() bool {
		if err := env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), nodeClass); err != nil {
			return false
		}
		imagesCondition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady)
		return imagesCondition.IsTrue()
	}).Should(BeTrue(), "NodeClass should have images ready before validation")

	// Get the available images from the NodeClass
	nodeImages, err := nodeClass.GetImages()
	Expect(err).ToNot(HaveOccurred(), "NodeClass should have images available after reconciliation")
	Expect(nodeImages).ToNot(BeEmpty(), "NodeClass should have at least one image after reconciliation")

	// Find the image that matches the provided reference
	var matchedImage *v1beta1.NodeImage
	for i, availableImage := range nodeImages {
		if availableImage.ID == imageReference {
			matchedImage = &nodeImages[i]
			break
		}
	}

	Expect(matchedImage).ToNot(BeNil(), "Actual VM image reference should exactly match one of the available NodeClass images: %s\nAvailable images: %v",
		imageReference, lo.Map(nodeImages, func(img v1beta1.NodeImage, _ int) string { return img.ID }))

	// Validate that ALL images in the NodeClass match the expected FIPS status
	fipsType := "non-FIPS"
	if expectFIPS {
		fipsType = "FIPS"
	}

	By(fmt.Sprintf("Validating that all NodeClass images are %s images", fipsType))
	for _, image := range nodeImages {
		lowerImageRef := strings.ToLower(image.ID)
		containsFIPS := strings.Contains(lowerImageRef, "fips")

		if expectFIPS {
			Expect(containsFIPS).To(BeTrue(),
				"Expected all NodeClass images to be FIPS images, but found non-FIPS image: %s", image.ID)
		} else {
			Expect(containsFIPS).To(BeFalse(),
				"Expected all NodeClass images to be non-FIPS images, but found FIPS image: %s", image.ID)
		}
	}
}
