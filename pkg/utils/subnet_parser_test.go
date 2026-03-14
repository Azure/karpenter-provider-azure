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

package utils

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCustomvnet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GetVnetSubnetIDComponents")
}

func Benchmark(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := GetVnetSubnetIDComponents("/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1")
		if err != nil {
			b.Fatal(err)
		}
	}
}

var _ = Describe("GetVnetSubnetIDComponents", func() {
	It("should return correct subnet id components", func() {
		subnetResource, err := GetVnetSubnetIDComponents("/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1")
		Expect(err).ToNot(HaveOccurred())
		subscriptionID := subnetResource.SubscriptionID
		resourceGroupName := subnetResource.ResourceGroupName
		vNetName := subnetResource.VNetName
		subnetName := subnetResource.SubnetName

		Expect(subscriptionID).To(Equal("00000000-0000-0000-0000-0000000000"))
		Expect(resourceGroupName).To(Equal("myrg"))
		Expect(vNetName).To(Equal("my-vnet"))
		Expect(subnetName).To(Equal("default1"))
	})
	It("should return error when unable to parse vnet subnet id", func() {
		// "/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1"
		customVnetSubnetID := "someSubnetID" // invalid format
		_, err := GetVnetSubnetIDComponents(customVnetSubnetID)
		Expect(err).To(HaveOccurred())

		// "resourceGr" instead of "resourceGroups" in customVnetSubnetID
		customVnetSubnetID = "/subscriptions/00000000-0000-0000-0000-0000000000/resourceGr/myrg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/default1"
		_, err = GetVnetSubnetIDComponents(customVnetSubnetID)
		Expect(err).To(HaveOccurred())
	})

	It("Is reflexive", func() {
		vnetsubnetid := GetSubnetResourceID("sam", "red", "violet", "subaru")
		vnet, err := GetVnetSubnetIDComponents(vnetsubnetid)
		Expect(err).To(BeNil())

		Expect(vnet.SubscriptionID).To(Equal("sam"))
		Expect(vnet.ResourceGroupName).To(Equal("red"))
		Expect(vnet.VNetName).To(Equal("violet"))
		Expect(vnet.SubnetName).To(Equal("subaru"))
	})

	It("real world weirdness (subnets is repeated broke old regex)", func() {
		vnetsubnetid := "/subscriptions/00000000-0000-0000-0000-0000000000/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/sillygeese-VNET/subnets/subnets/AKSMgmtv2-Subnet"
		_, err := GetVnetSubnetIDComponents(vnetsubnetid)
		Expect(err).ToNot(BeNil())
	})

	It("Is case insensitive (subnetparser.GetVnetSubnetIDComponents)", func() {
		vnetsubnetid := "/SubscRiptionS/mySubscRiption/ResourceGroupS/myResourceGroup/ProviDerS/MicrOsofT.NetWorK/VirtualNetwOrkS/myVirtualNetwork/SubNetS/mySubnet"
		vnet, err := GetVnetSubnetIDComponents(vnetsubnetid)
		Expect(err).ToNot(HaveOccurred())
		Expect(vnet.SubscriptionID).To(Equal("mySubscRiption"))
		Expect(vnet.ResourceGroupName).To(Equal("myResourceGroup"))
		Expect(vnet.VNetName).To(Equal("myVirtualNetwork"))
		Expect(vnet.SubnetName).To(Equal("mySubnet"))
	})

	It("Fails when appropriate", func() {
		_, err := GetVnetSubnetIDComponents("what/a/bunch/of/junk")
		Expect(err).ToNot(BeNil())
		_, err = GetVnetSubnetIDComponents("/subscriptions/sam/resourceGroups/red/providers/Microsoft.Network/virtualNetworks/soclose")
		Expect(err).ToNot(BeNil())
	})

	It("Test GetVNETSubnetIDComponents", func() {
		vnetSubnetID := "/subscriptions/SUB_ID/resourceGroups/RG_NAME/providers/Microsoft.Network/virtualNetworks/VNET_NAME/subnets/SUBNET_NAME"
		vs, err := GetVnetSubnetIDComponents(vnetSubnetID)
		Expect(err).To(BeNil())
		Expect(vs.SubscriptionID).To(Equal("SUB_ID"))
		Expect(vs.ResourceGroupName).To(Equal("RG_NAME"))
		Expect(vs.VNetName).To(Equal("VNET_NAME"))
		Expect(vs.SubnetName).To(Equal("SUBNET_NAME"))

		// case-insensitive match
		vnetSubnetID = "/SubscriPtioNS/SUB_ID/REsourceGroupS/RG_NAME/ProViderS/MicrosoFT.NetWorK/VirtualNetWorKS/VNET_NAME/SubneTS/SUBNET_NAME"
		vs, err = GetVnetSubnetIDComponents(vnetSubnetID)
		Expect(err).To(BeNil())
		Expect(vs.SubscriptionID).To(Equal("SUB_ID"))
		Expect(vs.ResourceGroupName).To(Equal("RG_NAME"))
		Expect(vs.VNetName).To(Equal("VNET_NAME"))
		Expect(vs.SubnetName).To(Equal("SUBNET_NAME"))

		//wtwo bad ones
		vnetSubnetID = "/providers/Microsoft.Network/virtualNetworks/VNET_NAME/subnets/SUBNET_NAME"
		_, err = GetVnetSubnetIDComponents(vnetSubnetID)
		Expect(err).ToNot(BeNil())

		vnetSubnetID = "badVnetSubnetID"
		_, err = GetVnetSubnetIDComponents(vnetSubnetID)
		Expect(err).ToNot(BeNil())
	})
})

var _ = Describe("IsSameVNET", func() {
	var baseResource VnetSubnetResource

	BeforeEach(func() {
		baseResource = VnetSubnetResource{
			SubscriptionID:    "12345678-1234-1234-1234-123456789012",
			ResourceGroupName: "my-resource-group",
			VNetName:          "my-vnet",
			SubnetName:        "my-subnet",
		}
	})

	DescribeTable("IsSameVNET comparison tests",
		func(compareResource VnetSubnetResource, expected bool) {
			Expect(baseResource.IsSameVNET(compareResource)).To(Equal(expected))
		},
		Entry("should return true when all VNET components match",
			VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "different-subnet",
			},
			true,
		),
		Entry("should return true when subnet names are different but VNET components match",
			VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "completely-different-subnet",
			},
			true,
		),
		Entry("should return false when subscription IDs are different",
			VnetSubnetResource{
				SubscriptionID:    "87654321-4321-4321-4321-210987654321",
				ResourceGroupName: "my-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "my-subnet",
			},
			false,
		),
		Entry("should return false when resource group names are different",
			VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "different-resource-group",
				VNetName:          "my-vnet",
				SubnetName:        "my-subnet",
			},
			false,
		),
		Entry("should return false when VNET names are different",
			VnetSubnetResource{
				SubscriptionID:    "12345678-1234-1234-1234-123456789012",
				ResourceGroupName: "my-resource-group",
				VNetName:          "different-vnet",
				SubnetName:        "my-subnet",
			},
			false,
		),
		Entry("should return false when multiple components are different",
			VnetSubnetResource{
				SubscriptionID:    "87654321-4321-4321-4321-210987654321",
				ResourceGroupName: "different-resource-group",
				VNetName:          "different-vnet",
				SubnetName:        "different-subnet",
			},
			false,
		),
	)

	Context("empty string comparisons", func() {
		It("should handle empty resource comparisons correctly", func() {
			emptyResource := VnetSubnetResource{
				SubscriptionID:    "",
				ResourceGroupName: "",
				VNetName:          "",
				SubnetName:        "",
			}

			Expect(emptyResource.IsSameVNET(emptyResource)).To(BeTrue())
			Expect(emptyResource.IsSameVNET(baseResource)).To(BeFalse())
			Expect(baseResource.IsSameVNET(emptyResource)).To(BeFalse())
		})
	})

	Context("case sensitivity", func() {
		DescribeTable("should be case-sensitive for all components",
			func(compareResource VnetSubnetResource) {
				Expect(baseResource.IsSameVNET(compareResource)).To(BeFalse())
			},
			Entry("different case resource group name",
				VnetSubnetResource{
					SubscriptionID:    "12345678-1234-1234-1234-123456789012",
					ResourceGroupName: "My-Resource-Group",
					VNetName:          "my-vnet",
					SubnetName:        "my-subnet",
				},
			),
			Entry("different case VNET name",
				VnetSubnetResource{
					SubscriptionID:    "12345678-1234-1234-1234-123456789012",
					ResourceGroupName: "my-resource-group",
					VNetName:          "My-VNet",
					SubnetName:        "my-subnet",
				},
			),
		)
	})
})
