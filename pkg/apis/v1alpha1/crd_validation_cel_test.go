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
	"strings"

	azv1alpha1 "github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/Pallinder/go-randomdata"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = AfterEach(func() {
	// Clean up all AzureNodeClasses created during tests
	nodeClassList := &azv1alpha1.AzureNodeClassList{}
	if err := env.Client.List(ctx, nodeClassList); err == nil {
		for i := range nodeClassList.Items {
			_ = env.Client.Delete(ctx, &nodeClassList.Items[i])
		}
	}
})

var _ = Describe("CEL/Validation", func() {
	var nodeClass *azv1alpha1.AzureNodeClass

	BeforeEach(func() {
		if env.Version.Minor() < 25 {
			Skip("CEL Validation is for 1.25>")
		}
		nodeClass = &azv1alpha1.AzureNodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
			Spec: azv1alpha1.AzureNodeClassSpec{
				ImageID:      lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/galleries/myGallery/images/myImage/versions/1.0.0"),
				VNETSubnetID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet"),
			},
		}
	})

	// --- imageID ---

	Context("imageID", func() {
		It("should accept a valid Compute Gallery image ID", func() {
			nodeClass.Spec.ImageID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/galleries/myGallery/images/myImage/versions/1.0.0")
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept a valid Community Gallery image ID", func() {
			nodeClass.Spec.ImageID = lo.ToPtr("/CommunityGalleries/AKSUbuntu-38d80f77/images/2204gen2/versions/2022.10.03")
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept a valid Shared Image Gallery ID", func() {
			nodeClass.Spec.ImageID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/images/myCustomImage")
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should reject an invalid imageID", func() {
			nodeClass.Spec.ImageID = lo.ToPtr("not-a-valid-image-id")
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should reject an imageID with a non-Compute provider", func() {
			nodeClass.Spec.ImageID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Storage/accounts/test")
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should accept nil imageID", func() {
			nodeClass.Spec.ImageID = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should be case-insensitive", func() {
			nodeClass.Spec.ImageID = lo.ToPtr("/SUBSCRIPTIONS/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/MY-RG/PROVIDERS/MICROSOFT.COMPUTE/GALLERIES/MYGALLERY/IMAGES/MYIMAGE/VERSIONS/1.0.0")
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- vnetSubnetID ---

	Context("vnetSubnetID", func() {
		It("should accept a valid subnet ID", func() {
			nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet")
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should reject an invalid subnet ID", func() {
			nodeClass.Spec.VNETSubnetID = lo.ToPtr("not-a-valid-subnet")
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should accept nil vnetSubnetID", func() {
			nodeClass.Spec.VNETSubnetID = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- osDiskSizeGB ---

	Context("osDiskSizeGB", func() {
		It("should accept a valid size within range", func() {
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(128))
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept minimum value (30)", func() {
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(30))
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept maximum value (4096)", func() {
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(4096))
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should reject below minimum", func() {
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(29))
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should reject above maximum", func() {
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(4097))
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should accept nil osDiskSizeGB", func() {
			nodeClass.Spec.OSDiskSizeGB = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- tags ---

	Context("tags", func() {
		It("should accept valid tags", func() {
			nodeClass.Spec.Tags = map[string]string{
				"environment": "production",
				"team":        "platform",
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should reject tags with key > 512 characters", func() {
			nodeClass.Spec.Tags = map[string]string{
				strings.Repeat("a", 513): "value",
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should reject tags with forbidden characters in key (<>%&?)", func() {
			for _, char := range []string{"<", ">", "%", "&", "?"} {
				nc := nodeClass.DeepCopy()
				nc.Name = strings.ToLower(randomdata.SillyName())
				nc.Spec.Tags = map[string]string{
					"bad" + char + "key": "value",
				}
				Expect(env.Client.Create(ctx, nc)).ToNot(Succeed(), "Expected tag key with %q to be rejected", char)
			}
		})
		It("should reject tags with backslash in key", func() {
			nodeClass.Spec.Tags = map[string]string{
				"bad\\key": "value",
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should reject tags with value > 256 characters", func() {
			nodeClass.Spec.Tags = map[string]string{
				"key": strings.Repeat("v", 257),
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should accept tags with value exactly 256 characters", func() {
			nodeClass.Spec.Tags = map[string]string{
				"key": strings.Repeat("v", 256),
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept empty tags", func() {
			nodeClass.Spec.Tags = map[string]string{}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept nil tags", func() {
			nodeClass.Spec.Tags = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- managedIdentities ---

	Context("managedIdentities", func() {
		It("should accept up to 10 identities", func() {
			identities := make([]string, 10)
			for i := range identities {
				identities[i] = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id-" + strings.Repeat("a", i+1)
			}
			nodeClass.Spec.ManagedIdentities = identities
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should reject more than 10 identities", func() {
			identities := make([]string, 11)
			for i := range identities {
				identities[i] = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id-" + strings.Repeat("a", i+1)
			}
			nodeClass.Spec.ManagedIdentities = identities
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
		It("should accept nil managedIdentities", func() {
			nodeClass.Spec.ManagedIdentities = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- security ---

	Context("security", func() {
		It("should accept encryptionAtHost=true", func() {
			nodeClass.Spec.Security = &azv1alpha1.AzureNodeClassSecurity{
				EncryptionAtHost: lo.ToPtr(true),
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept encryptionAtHost=false", func() {
			nodeClass.Spec.Security = &azv1alpha1.AzureNodeClassSecurity{
				EncryptionAtHost: lo.ToPtr(false),
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept nil security", func() {
			nodeClass.Spec.Security = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- userData ---

	Context("userData", func() {
		It("should accept valid base64 userData", func() {
			nodeClass.Spec.UserData = lo.ToPtr("IyEvYmluL2Jhc2gKZWNobyAiaGVsbG8gd29ybGQi")
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
		It("should accept nil userData", func() {
			nodeClass.Spec.UserData = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})

	// --- empty spec ---

	Context("empty spec", func() {
		It("should accept a completely empty spec", func() {
			nodeClass.Spec = azv1alpha1.AzureNodeClassSpec{}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})
})
