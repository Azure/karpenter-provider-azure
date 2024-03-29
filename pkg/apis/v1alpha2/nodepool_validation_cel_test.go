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

package v1alpha2_test

import (
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Pallinder/go-randomdata"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/karpenter/pkg/apis/v1beta1"
)

var _ = Describe("CEL/Validation", func() {
	var nodePool *v1beta1.NodePool

	BeforeEach(func() {
		if env.Version.Minor() < 25 {
			Skip("CEL Validation is for 1.25>")
		}
		nodePool = &v1beta1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
			Spec: v1beta1.NodePoolSpec{
				Template: v1beta1.NodeClaimTemplate{
					Spec: v1beta1.NodeClaimSpec{
						NodeClassRef: &v1beta1.NodeClassReference{
							Kind: "NodeClaim",
							Name: "default",
						},
						Requirements: []v1.NodeSelectorRequirement{
							{
								Key:      v1beta1.CapacityTypeLabelKey,
								Operator: v1.NodeSelectorOpExists,
							},
						},
					},
				},
			},
		}
	})
	Context("Requirements", func() {
		It("should allow restricted domains exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1beta1.LabelDomainExceptions {
				nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirement{
					{Key: label + "/test", Operator: v1.NodeSelectorOpIn, Values: []string{"test"}},
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate()).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should allow well known label exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1beta1.WellKnownLabels.Difference(sets.New(v1beta1.NodePoolLabelKey)) {
				nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirement{
					{Key: label, Operator: v1.NodeSelectorOpIn, Values: []string{"test"}},
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate()).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should not allow internal labels", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1alpha2.RestrictedLabels {
				nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirement{
					{Key: label, Operator: v1.NodeSelectorOpIn, Values: []string{"test"}},
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate()).ToNot(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
	})
	Context("VnetSubnetID", func() {
		DescribeTable("should allow valid VnetSubnetID", func(vnetSubnetID string, expected bool) {
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					VnetSubnetID: &vnetSubnetID,
				},
			}
			if expected {
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			} else {
				Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
			}
		},
			Entry("valid VnetSubnetID", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", true),
			Entry("should allow mixed casing in all the names", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgName/providers/Microsoft.Network/virtualNetworks/vnetName/subnets/subnetName", true),
			Entry("valid format with different subnet name", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/virtualNetworks/vnet/subnets/anotherSubnet", true),
			Entry("valid format with uppercase subnet name", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/virtualNetworks/vnet/subnets/SUBNET", true),
			Entry("valid format with mixed-case resource group and subnet name", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/MyResourceGroup/providers/Microsoft.Network/virtualNetworks/MyVirtualNetwork/subnets/MySubnet", true),
			Entry("invalid subnet with too short ID", "/subscriptions/123/resourceGroups/rgname/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
			Entry("missing resourceGroups in path", "/subscriptions/12345678-1234-1234-1234-123456789012/rgname/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
			Entry("invalid provider in path", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Storage/virtualNetworks/vnet/subnets/subnet", false),
			Entry("missing virtualNetworks in path", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/subnets/subnet", false),
			Entry("valid VnetSubnetID at max length", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/"+strings.Repeat("a", 63)+"/providers/Microsoft.Network/virtualNetworks/"+strings.Repeat("b", 63)+"/subnets/"+strings.Repeat("c", 63), true),
			Entry("subnet name too long", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/virtualNetworks/vnetname/subnets/"+strings.Repeat("d", 64), false),
			Entry("VNET name too long", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/virtualNetworks/"+strings.Repeat("e", 64)+"/subnets/subnet", false),
			Entry("resource group name too long", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/"+strings.Repeat("f", 64)+"/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
		)
	})
	Context("Labels", func() {
		It("should allow restricted domains exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1beta1.LabelDomainExceptions {
				nodePool.Spec.Template.Labels = map[string]string{
					label: "test",
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate()).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should allow well known label exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1beta1.WellKnownLabels.Difference(sets.New(v1beta1.NodePoolLabelKey)) {
				nodePool.Spec.Template.Labels = map[string]string{
					label: "test",
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate()).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should not allow internal labels", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1alpha2.RestrictedLabels {
				nodePool.Spec.Template.Labels = map[string]string{
					label: "test",
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate()).ToNot(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
	})
})
