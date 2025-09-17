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
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("CEL/Validation", func() {
	var nodePool *karpv1.NodePool

	BeforeEach(func() {
		if env.Version.Minor() < 25 {
			Skip("CEL Validation is for 1.25>")
		}
		nodePool = &karpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
			Spec: karpv1.NodePoolSpec{
				Template: karpv1.NodeClaimTemplate{
					Spec: karpv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Group: "karpenter.azure.com",
							Kind:  "AKSNodeClass",
							Name:  "default",
						},
						Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
							{
								NodeSelectorRequirement: corev1.NodeSelectorRequirement{
									Key:      karpv1.CapacityTypeLabelKey,
									Operator: corev1.NodeSelectorOpExists,
								},
							},
						},
					},
				},
			},
		}
	})
	Context("VnetSubnetID", func() {
		DescribeTable("Should only accept valid VnetSubnetID", func(vnetSubnetID string, expected bool) {
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					VNETSubnetID: &vnetSubnetID,
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
			Entry("missing resourceGroups in path", "/subscriptions/12345678-1234-1234-1234-123456789012/rgname/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
			Entry("invalid provider in path", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Storage/virtualNetworks/vnet/subnets/subnet", false),
			Entry("missing virtualNetworks in path", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rgname/providers/Microsoft.Network/subnets/subnet", false),
			Entry("valid VnetSubnetID at max length", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/"+strings.Repeat("a", 63)+"/providers/Microsoft.Network/virtualNetworks/"+strings.Repeat("b", 63)+"/subnets/"+strings.Repeat("c", 63), true),
			Entry("valid resource group name 'my-resource_group'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-resource_group/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", true),
			Entry("valid resource group name starting with dot '.starting.with.dot'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/.starting.with.dot/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", true),
			Entry("valid resource group name ending with hyphen 'ends-with-hyphen-'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/ends-with-hyphen-/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", true),
			Entry("valid resource group name with parentheses 'contains.(parentheses)'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/contains.(parentheses)/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", true),
			Entry("valid resource group name 'valid.name-with-multiple.characters'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/valid.name-with-multiple.characters/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", true),
			Entry("invalid resource group name ending with dot 'ends.with.dot.'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/ends.with.dot./providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
			Entry("invalid resource group name with invalid character 'invalid#character'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/invalid#character/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
			Entry("invalid resource group name with unsupported chars 'name@with*unsupported&chars'", "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/name@with*unsupported&chars/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet", false),
		)
	})

	Context("ImageFamily", func() {
		It("should reject invalid ImageFamily", func() {
			invalidImageFamily := "123"
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					ImageFamily: &invalidImageFamily,
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
	})

	Context("FIPSMode", func() {
		It("should reject invalid FIPSMode", func() {
			invalidFIPSMode := v1alpha2.FIPSMode("123")
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					FIPSMode: &invalidFIPSMode,
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
	})

	Context("OSDiskSizeGB", func() {
		DescribeTable("Should validate OSDiskSizeGB constraints", func(osDiskSizeGB *int32, expected bool) {
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					OSDiskSizeGB: osDiskSizeGB,
				},
			}
			if expected {
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			} else {
				Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
			}
		},
			Entry("valid minimum size (30 GB)", lo.ToPtr(int32(30)), true),
			Entry("valid default size (128 GB)", lo.ToPtr(int32(128)), true),
			Entry("valid large size (1024 GB)", lo.ToPtr(int32(1024)), true),
			Entry("valid maximum size (2048 GB)", lo.ToPtr(int32(2048)), true),
			Entry("nil value (uses default)", nil, true),
			Entry("below minimum (29 GB)", lo.ToPtr(int32(29)), false),
			Entry("above maximum (2049 GB)", lo.ToPtr(int32(2049)), false),
			Entry("well above maximum (4096 GB)", lo.ToPtr(int32(4096)), false),
		)
	})

	Context("ImageFamily and FIPSMode", func() {
		DescribeTable("should only accept valid ImageFamily and FIPSMode combinations", func(imageFamily string, fipsMode *v1alpha2.FIPSMode, expected bool) {
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec:       v1alpha2.AKSNodeClassSpec{},
			}
			// allows for leaving imageFamily unset, which defaults to Ubuntu
			if imageFamily != "" {
				nodeClass.Spec.ImageFamily = &imageFamily
			}
			nodeClass.Spec.FIPSMode = fipsMode
			if expected {
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			} else {
				Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
			}
		},
			Entry("generic Ubuntu when FIPSMode is explicitly Disabled should succeed", v1alpha2.UbuntuImageFamily, &v1alpha2.FIPSModeDisabled, true),
			Entry("generic Ubuntu when FIPSMode is not explicitly set should succeed", v1alpha2.UbuntuImageFamily, nil, true),
			Entry("generic Ubuntu when FIPSMode is explicitly FIPS should succeed", v1alpha2.UbuntuImageFamily, &v1alpha2.FIPSModeFIPS, true),
			Entry("Ubuntu2204 when FIPSMode is explicitly Disabled should succeed", v1alpha2.Ubuntu2204ImageFamily, &v1alpha2.FIPSModeDisabled, true),
			Entry("Ubuntu2204 when FIPSMode is not explicitly set should succeed", v1alpha2.Ubuntu2204ImageFamily, nil, true),
			//TODO: Modify when Ubuntu 22.04 with FIPS becomes available
			Entry("Ubuntu2204 when FIPSMode is explicitly FIPS should fail", v1alpha2.Ubuntu2204ImageFamily, &v1alpha2.FIPSModeFIPS, false),
			Entry("Ubuntu2404 when FIPSMode is explicitly Disabled should succeed", v1alpha2.Ubuntu2404ImageFamily, &v1alpha2.FIPSModeDisabled, true),
			Entry("Ubuntu2404 when FIPSMode is not explicitly set should succeed", v1alpha2.Ubuntu2404ImageFamily, nil, true),
			//TODO: Modify when Ubuntu 24.04 with FIPS becomes available
			Entry("Ubuntu2404 when FIPSMode is explicitly FIPS should fail", v1alpha2.Ubuntu2404ImageFamily, &v1alpha2.FIPSModeFIPS, false),
			Entry("generic AzureLinux when FIPSMode is explicitly Disabled should succeed", v1alpha2.AzureLinuxImageFamily, &v1alpha2.FIPSModeDisabled, true),
			Entry("generic AzureLinux when FIPSMode is not explicitly set should succeed", v1alpha2.AzureLinuxImageFamily, nil, true),
			Entry("generic AzureLinux when FIPSMode is explicitly FIPS should succeed", v1alpha2.AzureLinuxImageFamily, &v1alpha2.FIPSModeFIPS, true),
			Entry("unspecified ImageFamily (defaults to Ubuntu) when FIPSMode is explicitly Disabled should succeed", "", &v1alpha2.FIPSModeDisabled, true),
			Entry("unspecified ImageFamily (defaults to Ubuntu) when FIPSMode is not explicitly set should succeed", "", nil, true),
			Entry("unspecified ImageFamily (defaults to Ubuntu) when FIPSMode is explicitly FIPS should succeed", "", &v1alpha2.FIPSModeFIPS, true),
		)
	})

	Context("Requirements", func() {
		It("should allow restricted domains exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range karpv1.LabelDomainExceptions {
				nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{NodeSelectorRequirement: corev1.NodeSelectorRequirement{Key: label + "/test", Operator: corev1.NodeSelectorOpIn, Values: []string{"test"}}},
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should allow well known label exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range karpv1.WellKnownLabels.Difference(sets.New(karpv1.NodePoolLabelKey, karpv1.CapacityTypeLabelKey)) {
				nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{NodeSelectorRequirement: corev1.NodeSelectorRequirement{Key: label, Operator: corev1.NodeSelectorOpIn, Values: []string{"test"}}},
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should fail validation with only invalid capacity types", func() {
			oldNodePool := nodePool.DeepCopy()
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"xspot"}, // Invalid value
				},
			})
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(nodePool.RuntimeValidate(ctx)).ToNot(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
			nodePool = oldNodePool.DeepCopy()
		})
		It("should pass validation with valid capacity types", func() {
			oldNodePool := nodePool.DeepCopy()
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand}, // Valid value
				},
			})
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
			nodePool = oldNodePool.DeepCopy()
		})
		It("should fail open if invalid and valid capacity types are present", func() {
			oldNodePool := nodePool.DeepCopy()
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand, "xspot"}, // Valid and invalid value
				},
			})
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
			nodePool = oldNodePool.DeepCopy()
		})
	})
	Context("Labels", func() {
		It("should allow restricted domains exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range karpv1.LabelDomainExceptions {
				nodePool.Spec.Template.Labels = map[string]string{
					label: "test",
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
		It("should allow well known label exceptions", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range karpv1.WellKnownLabels.Difference(sets.New(karpv1.NodePoolLabelKey)) {
				nodePool.Spec.Template.Labels = map[string]string{
					label: "test",
				}
				Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
				Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
				Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
	})

	Context("Taints", func() {
		It("should allow taints with non-kubernetes.azure.com domains", func() {
			nodePool.Spec.Template.Spec.Taints = []corev1.Taint{
				{
					Key:    "example.com/custom-taint",
					Value:  "true",
					Effect: corev1.TaintEffectNoSchedule,
				},
				{
					Key:    "company.io/another-taint",
					Value:  "value",
					Effect: corev1.TaintEffectPreferNoSchedule,
				},
			}
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
		})

		It("should allow taints with allowed kubernetes.azure.com keys", func() {
			nodePool.Spec.Template.Spec.Taints = []corev1.Taint{
				{
					Key:    "kubernetes.azure.com/scalesetpriority",
					Value:  "spot",
					Effect: corev1.TaintEffectNoSchedule,
				},
				{
					Key:    "kubernetes.azure.com/mode",
					Value:  "gateway",
					Effect: corev1.TaintEffectNoSchedule,
				},
			}
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
		})

		It("should reject taints with disallowed kubernetes.azure.com keys", func() {
			nodePool.Spec.Template.Spec.Taints = []corev1.Taint{
				{
					Key:    "kubernetes.azure.com/custom-key",
					Value:  "value",
					Effect: corev1.TaintEffectNoSchedule,
				},
			}
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
		})
	})

	Context("StartupTaints", func() {
		It("should allow startup taints with non-kubernetes.azure.com domains", func() {
			nodePool.Spec.Template.Spec.StartupTaints = []corev1.Taint{
				{
					Key:    "example.com/startup-taint",
					Value:  "true",
					Effect: corev1.TaintEffectNoSchedule,
				},
				{
					Key:    "company.io/initialization",
					Value:  "pending",
					Effect: corev1.TaintEffectPreferNoSchedule,
				},
			}
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
		})

		DescribeTable("should reject startup taints with kubernetes.azure.com domain", func(key, value string, effect corev1.TaintEffect) {
			nodePool.Spec.Template.Spec.StartupTaints = []corev1.Taint{
				{
					Key:    key,
					Value:  value,
					Effect: effect,
				},
			}
			Expect(env.Client.Create(ctx, nodePool)).To(Succeed())
			Expect(nodePool.RuntimeValidate(ctx)).To(Succeed())
			Expect(env.Client.Delete(ctx, nodePool)).To(Succeed())
		},
			Entry("allowed key kubernetes.azure.com/scalesetpriority in regular taints", "kubernetes.azure.com/scalesetpriority", "spot", corev1.TaintEffectNoSchedule),
			Entry("allowed key kubernetes.azure.com/mode in regular taints", "kubernetes.azure.com/mode", "gateway", corev1.TaintEffectNoSchedule),
			Entry("custom kubernetes.azure.com key", "kubernetes.azure.com/custom-startup", "value", corev1.TaintEffectPreferNoSchedule),
		)
	})

	Context("Tags", func() {
		It("should allow tags with valid keys and values", func() {
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					Tags: map[string]string{
						"valid-key":  "valid-value",
						"anotherKey": "anotherValue",
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})

		DescribeTable(
			"should reject tags with invalid keys",
			func(key string) {
				nodeClass := &v1alpha2.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1alpha2.AKSNodeClassSpec{
						Tags: map[string]string{
							key: "value",
						},
					},
				}
				Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
			},
			Entry("key contains <", "invalid<key"),
			Entry("key contains >", "invalid>key"),
			Entry("key contains %", "invalid%key"),
			Entry("key contains &", "invalid&key"),
			Entry(`key contains \`, `invalid\key`),
			Entry("key contains ?", "invalid?key"),
			Entry("key exceeds max length", strings.Repeat("a", 513)),
		)

		DescribeTable(
			"should reject tags with invalid values",
			func(value string) {
				nodeClass := &v1alpha2.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1alpha2.AKSNodeClassSpec{
						Tags: map[string]string{
							"valid-key": value,
						},
					},
				}
				Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
			},
			Entry("value exceeds max length", strings.Repeat("b", 257)),
		)

		It("should allow tags with keys and values at max valid length", func() {
			nodeClass := &v1alpha2.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1alpha2.AKSNodeClassSpec{
					Tags: map[string]string{
						strings.Repeat("a", 512): strings.Repeat("b", 256),
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})
})
