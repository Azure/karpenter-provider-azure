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

package v1alpha1_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/samber/lo"
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AzureNodeClass")
}

var _ = Describe("AzureNodeClass", func() {
	var azureNC *v1alpha1.AzureNodeClass

	BeforeEach(func() {
		azureNC = &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID: "/CommunityGalleries/test/images/ubuntu/versions/latest",
			},
		}
	})

	Describe("Hash", func() {
		It("should produce consistent hash", func() {
			h1 := azureNC.Hash()
			h2 := azureNC.Hash()
			Expect(h1).To(Equal(h2))
		})

		It("should produce different hash when spec changes", func() {
			h1 := azureNC.Hash()
			azureNC.Spec.OSDiskSizeGB = lo.ToPtr(int32(256))
			h2 := azureNC.Hash()
			Expect(h1).ToNot(Equal(h2))
		})

		It("should ignore tags in hash", func() {
			h1 := azureNC.Hash()
			azureNC.Spec.Tags = map[string]string{"key": "value"}
			h2 := azureNC.Hash()
			Expect(h1).To(Equal(h2))
		})
	})

	Describe("GetEncryptionAtHost", func() {
		It("should return false when security is nil", func() {
			Expect(azureNC.GetEncryptionAtHost()).To(BeFalse())
		})

		It("should return false when encryptionAtHost is nil", func() {
			azureNC.Spec.Security = &v1alpha1.AzureNodeClassSecurity{}
			Expect(azureNC.GetEncryptionAtHost()).To(BeFalse())
		})

		It("should return true when encryptionAtHost is true", func() {
			azureNC.Spec.Security = &v1alpha1.AzureNodeClassSecurity{
				EncryptionAtHost: lo.ToPtr(true),
			}
			Expect(azureNC.GetEncryptionAtHost()).To(BeTrue())
		})
	})

	Describe("StatusConditions", func() {
		It("should return condition set", func() {
			cs := azureNC.StatusConditions()
			Expect(cs).ToNot(BeNil())
		})
	})

	Describe("DeepCopy", func() {
		It("should deep copy the object", func() {
			azureNC.Spec.SubscriptionID = lo.ToPtr("test-sub-id")
			azureNC.Spec.ManagedIdentities = []string{"/subscriptions/test/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"}
			azureNC.Spec.Tags = map[string]string{"env": "test"}
			azureNC.Spec.Security = &v1alpha1.AzureNodeClassSecurity{
				EncryptionAtHost: lo.ToPtr(true),
			}

			copied := azureNC.DeepCopy()
			Expect(copied.Spec.ImageID).To(Equal(azureNC.Spec.ImageID))
			Expect(*copied.Spec.SubscriptionID).To(Equal(*azureNC.Spec.SubscriptionID))
			Expect(copied.Spec.ManagedIdentities).To(Equal(azureNC.Spec.ManagedIdentities))
			Expect(copied.Spec.Tags).To(Equal(azureNC.Spec.Tags))
			Expect(*copied.Spec.Security.EncryptionAtHost).To(BeTrue())

			// Verify deep copy independence
			*copied.Spec.SubscriptionID = "modified"
			Expect(*azureNC.Spec.SubscriptionID).To(Equal("test-sub-id"))
		})
	})
})
