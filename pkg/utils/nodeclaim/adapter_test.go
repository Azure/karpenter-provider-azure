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

package nodeclaim

import (
	"encoding/base64"
	"testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/samber/lo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NodeClaim Adapter")
}

var _ = Describe("AKSNodeClassFromAzureNodeClass", func() {
	It("should map basic fields", func() {
		azureNC := &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID:      "/CommunityGalleries/test/images/ubuntu/versions/latest",
				OSDiskSizeGB: lo.ToPtr(int32(256)),
				VNETSubnetID: lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"),
				Tags:         map[string]string{"env": "test"},
			},
		}
		azureNC.Name = "test-nc"

		aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

		Expect(aksNC.Name).To(Equal("test-nc"))
		Expect(*aksNC.Spec.ImageID).To(Equal(azureNC.Spec.ImageID))
		Expect(*aksNC.Spec.OSDiskSizeGB).To(Equal(int32(256)))
		Expect(*aksNC.Spec.VNETSubnetID).To(Equal(*azureNC.Spec.VNETSubnetID))
		Expect(aksNC.Spec.Tags).To(Equal(map[string]string{"env": "test"}))
	})

	It("should base64 encode userData", func() {
		plainText := "#!/bin/bash\necho hello"
		azureNC := &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID:  "/test/image",
				UserData: &plainText,
			},
		}

		aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

		Expect(aksNC.Spec.UserData).ToNot(BeNil())
		decoded, err := base64.StdEncoding.DecodeString(*aksNC.Spec.UserData)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(decoded)).To(Equal(plainText))
	})

	It("should map managed identities", func() {
		azureNC := &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID: "/test/image",
				ManagedIdentities: []string{
					"/subscriptions/sub/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1",
					"/subscriptions/sub/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id2",
				},
			},
		}

		aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

		Expect(aksNC.Spec.ManagedIdentities).To(HaveLen(2))
	})

	It("should map security settings", func() {
		azureNC := &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID: "/test/image",
				Security: &v1alpha1.AzureNodeClassSecurity{
					EncryptionAtHost: lo.ToPtr(true),
				},
			},
		}

		aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

		Expect(aksNC.Spec.Security).ToNot(BeNil())
		Expect(*aksNC.Spec.Security.EncryptionAtHost).To(BeTrue())
	})

	It("should map additional adapter fields", func() {
		azureNC := &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID:        "/test/image",
				DataDiskSizeGB: lo.ToPtr(int32(512)),
				SubscriptionID: lo.ToPtr("00000000-0000-0000-0000-000000000001"),
				ResourceGroup:  lo.ToPtr("my-rg"),
				Location:       lo.ToPtr("eastus"),
				InstanceTypes:  []string{"Standard_D4s_v5", "Standard_D8s_v5"},
			},
		}

		aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

		Expect(*aksNC.Spec.DataDiskSizeGB).To(Equal(int32(512)))
		Expect(*aksNC.Spec.SubscriptionID).To(Equal("00000000-0000-0000-0000-000000000001"))
		Expect(*aksNC.Spec.ResourceGroup).To(Equal("my-rg"))
		Expect(*aksNC.Spec.Location).To(Equal("eastus"))
		Expect(aksNC.Spec.InstanceTypes).To(Equal([]string{"Standard_D4s_v5", "Standard_D8s_v5"}))
	})

	It("should handle nil optional fields", func() {
		azureNC := &v1alpha1.AzureNodeClass{
			Spec: v1alpha1.AzureNodeClassSpec{
				ImageID: "/test/image",
			},
		}

		aksNC := AKSNodeClassFromAzureNodeClass(azureNC)

		Expect(aksNC.Spec.UserData).To(BeNil())
		Expect(aksNC.Spec.ManagedIdentities).To(BeNil())
		Expect(aksNC.Spec.DataDiskSizeGB).To(BeNil())
		Expect(aksNC.Spec.SubscriptionID).To(BeNil())
		Expect(aksNC.Spec.ResourceGroup).To(BeNil())
		Expect(aksNC.Spec.Location).To(BeNil())
		Expect(aksNC.Spec.InstanceTypes).To(BeNil())
		Expect(aksNC.Spec.Security).To(BeNil())
		Expect(aksNC.Spec.VNETSubnetID).To(BeNil())
		Expect(aksNC.Spec.OSDiskSizeGB).To(BeNil())
	})
})
