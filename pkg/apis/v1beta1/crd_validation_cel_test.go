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

package v1beta1_test

import (
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Pallinder/go-randomdata"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("CEL/Validation", func() {
	var nodePool *karpv1.NodePool

	// Helper function to create a complete LocalDNSOverrides with all required fields
	// Use forwardToVnetDNS=true for root zone "." in vnetDNSOverrides
	createCompleteLocalDNSOverrides := func(forwardToVnetDNS bool) *v1beta1.LocalDNSOverrides {
		forwardDest := v1beta1.LocalDNSForwardDestinationClusterCoreDNS
		if forwardToVnetDNS {
			forwardDest = v1beta1.LocalDNSForwardDestinationVnetDNS
		}
		return &v1beta1.LocalDNSOverrides{
			QueryLogging:       lo.ToPtr(v1beta1.LocalDNSQueryLoggingError),
			Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolPreferUDP),
			ForwardDestination: lo.ToPtr(forwardDest),
			ForwardPolicy:      lo.ToPtr(v1beta1.LocalDNSForwardPolicySequential),
			MaxConcurrent:      lo.ToPtr(int32(100)),
			CacheDuration:      karpv1.MustParseNillableDuration("1h"),
			ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
			ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleVerify),
		}
	}

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
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
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
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					ImageFamily: &invalidImageFamily,
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
	})

	Context("FIPSMode", func() {
		It("should reject invalid FIPSMode", func() {
			invalidFIPSMode := v1beta1.FIPSMode("123")
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					FIPSMode: &invalidFIPSMode,
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).ToNot(Succeed())
		})
	})

	Context("LocalDNS", func() {
		It("should accept when LocalDNS is completely omitted", func() {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec:       v1beta1.AKSNodeClassSpec{
					// LocalDNS is nil - should be accepted
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})

		It("should accept complete LocalDNS configuration with all required fields", func() {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
							".":             createCompleteLocalDNSOverrides(true),
							"cluster.local": createCompleteLocalDNSOverrides(false),
						},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
							".":             createCompleteLocalDNSOverrides(false),
							"cluster.local": createCompleteLocalDNSOverrides(false),
						},
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})

		DescribeTable("should reject partial LocalDNS configurations",
			func(buildLocalDNS func() *v1beta1.LocalDNS) {
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: buildLocalDNS(),
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Required value"))
			},
			Entry("only Mode provided", func() *v1beta1.LocalDNS {
				return &v1beta1.LocalDNS{
					Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				}
			}),
			Entry("only VnetDNSOverrides provided", func() *v1beta1.LocalDNS {
				return &v1beta1.LocalDNS{
					VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
						".":             createCompleteLocalDNSOverrides(true),
						"cluster.local": createCompleteLocalDNSOverrides(false),
					},
				}
			}),
			Entry("only KubeDNSOverrides provided", func() *v1beta1.LocalDNS {
				return &v1beta1.LocalDNS{
					KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
						".":             createCompleteLocalDNSOverrides(false),
						"cluster.local": createCompleteLocalDNSOverrides(false),
					},
				}
			}),
		)

		DescribeTable("should reject partial LocalDNSOverrides configurations",
			func(modifyOverrides func(*v1beta1.LocalDNSOverrides)) {
				overrides := createCompleteLocalDNSOverrides(true)
				modifyOverrides(overrides)
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             overrides,
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Required value"))
			},
			Entry("missing QueryLogging", func(o *v1beta1.LocalDNSOverrides) { o.QueryLogging = nil }),
			Entry("missing Protocol", func(o *v1beta1.LocalDNSOverrides) { o.Protocol = nil }),
			Entry("missing ForwardDestination", func(o *v1beta1.LocalDNSOverrides) { o.ForwardDestination = nil }),
			Entry("missing ForwardPolicy", func(o *v1beta1.LocalDNSOverrides) { o.ForwardPolicy = nil }),
			Entry("missing MaxConcurrent", func(o *v1beta1.LocalDNSOverrides) { o.MaxConcurrent = nil }),
			Entry("missing ServeStale", func(o *v1beta1.LocalDNSOverrides) { o.ServeStale = nil }),
		)

		DescribeTable("should validate LocalDNSMode", func(mode *v1beta1.LocalDNSMode, expectedErr string) {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             mode,
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false)},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid mode: Preferred", lo.ToPtr(v1beta1.LocalDNSModePreferred), ""),
			Entry("valid mode: Required", lo.ToPtr(v1beta1.LocalDNSModeRequired), ""),
			Entry("valid mode: Disabled", lo.ToPtr(v1beta1.LocalDNSModeDisabled), ""),
			Entry("invalid mode: invalid-string", lo.ToPtr(v1beta1.LocalDNSMode("invalid-string")), "spec.localDNS.mode"),
			Entry("invalid mode: empty", lo.ToPtr(v1beta1.LocalDNSMode("")), "spec.localDNS.mode"),
		)

		DescribeTable("should validate LocalDNSQueryLogging", func(queryLogging *v1beta1.LocalDNSQueryLogging, expectedErr string) {
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.QueryLogging = queryLogging
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid query logging: Error", lo.ToPtr(v1beta1.LocalDNSQueryLoggingError), ""),
			Entry("valid query logging: Log", lo.ToPtr(v1beta1.LocalDNSQueryLoggingLog), ""),
			Entry("invalid query logging: invalid-string", lo.ToPtr(v1beta1.LocalDNSQueryLogging("invalid-string")), "spec.localDNS.vnetDNSOverrides"),
			Entry("invalid query logging: empty", lo.ToPtr(v1beta1.LocalDNSQueryLogging("")), "spec.localDNS.vnetDNSOverrides"),
		)

		DescribeTable("should validate LocalDNSProtocol", func(protocol *v1beta1.LocalDNSProtocol, expectedErr string) {
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.Protocol = protocol
			// When using ForceTCP, we can't use ServeStaleVerify, so use Immediate instead
			if protocol != nil && *protocol == v1beta1.LocalDNSProtocolForceTCP {
				overrideConfig.ServeStale = lo.ToPtr(v1beta1.LocalDNSServeStaleImmediate)
			}
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid protocol: PreferUDP", lo.ToPtr(v1beta1.LocalDNSProtocolPreferUDP), ""),
			Entry("valid protocol: ForceTCP", lo.ToPtr(v1beta1.LocalDNSProtocolForceTCP), ""),
			Entry("invalid protocol: invalid-string", lo.ToPtr(v1beta1.LocalDNSProtocol("invalid-string")), "spec.localDNS.vnetDNSOverrides"),
			Entry("invalid protocol: empty", lo.ToPtr(v1beta1.LocalDNSProtocol("")), "spec.localDNS.vnetDNSOverrides"),
		)

		DescribeTable("should validate LocalDNSForwardDestination", func(forwardDestination *v1beta1.LocalDNSForwardDestination, expectedErr string) {
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.ForwardDestination = forwardDestination
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid forward destination: ClusterCoreDNS", lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS), ""),
			Entry("valid forward destination: VnetDNS", lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS), ""),
			Entry("invalid forward destination: invalid-string", lo.ToPtr(v1beta1.LocalDNSForwardDestination("invalid-string")), "spec.localDNS.vnetDNSOverrides"),
			Entry("invalid forward destination: empty", lo.ToPtr(v1beta1.LocalDNSForwardDestination("")), "spec.localDNS.vnetDNSOverrides"),
		)

		DescribeTable("should validate LocalDNSForwardPolicy", func(forwardPolicy *v1beta1.LocalDNSForwardPolicy, expectedErr string) {
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.ForwardPolicy = forwardPolicy
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid forward policy: Sequential", lo.ToPtr(v1beta1.LocalDNSForwardPolicySequential), ""),
			Entry("valid forward policy: RoundRobin", lo.ToPtr(v1beta1.LocalDNSForwardPolicyRoundRobin), ""),
			Entry("valid forward policy: Random", lo.ToPtr(v1beta1.LocalDNSForwardPolicyRandom), ""),
			Entry("invalid forward policy: invalid-string", lo.ToPtr(v1beta1.LocalDNSForwardPolicy("invalid-string")), "spec.localDNS.vnetDNSOverrides"),
			Entry("invalid forward policy: empty", lo.ToPtr(v1beta1.LocalDNSForwardPolicy("")), "spec.localDNS.vnetDNSOverrides"),
		)

		DescribeTable("should validate LocalDNSServeStale", func(serveStale *v1beta1.LocalDNSServeStale, expectedErr string) {
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.ServeStale = serveStale
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid serve stale: Verify", lo.ToPtr(v1beta1.LocalDNSServeStaleVerify), ""),
			Entry("valid serve stale: Immediate", lo.ToPtr(v1beta1.LocalDNSServeStaleImmediate), ""),
			Entry("valid serve stale: Disable", lo.ToPtr(v1beta1.LocalDNSServeStaleDisable), ""),
			Entry("invalid serve stale: invalid-string", lo.ToPtr(v1beta1.LocalDNSServeStale("invalid-string")), "spec.localDNS.vnetDNSOverrides"),
			Entry("invalid serve stale: empty", lo.ToPtr(v1beta1.LocalDNSServeStale("")), "spec.localDNS.vnetDNSOverrides"),
		)

		DescribeTable("should validate CacheDuration", func(durationStr string, expectedErr string) {
			cacheDuration := karpv1.MustParseNillableDuration(durationStr)
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.CacheDuration = cacheDuration
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid duration: 1h", "1h", ""),
			Entry("valid duration: 30m", "30m", ""),
			Entry("valid duration: 60s", "60s", ""),
			Entry("valid duration: 1h30m", "1h30m", ""),
			Entry("valid duration: 2h15m30s", "2h15m30s", ""),
		)

		DescribeTable("should reject invalid duration values", func(patchJSON string, expectedErr string) {
			// Test using unstructured to bypass Go type parsing
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": createCompleteLocalDNSOverrides(false)},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			// Create the object first
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// Now try to patch with an invalid value using raw JSON
			patch := []byte(patchJSON)
			err := env.Client.Patch(ctx, nodeClass, client.RawPatch(client.Merge.Type(), patch))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedErr))
		},
			Entry("CacheDuration with decimal values", `{"spec":{"localDNS":{"vnetDNSOverrides":{"test.domain":{"cacheDuration":"5.5h"}}}}}`, "\"5.5h\": spec.localDNS.vnetDNSOverrides.test.domain.cacheDuration in body should match '^([0-9]+(s|m|h))+$'"),
			Entry("CacheDuration with 'Never' value", `{"spec":{"localDNS":{"vnetDNSOverrides":{"test.domain":{"cacheDuration":"Never"}}}}}`, "\"Never\": spec.localDNS.vnetDNSOverrides.test.domain.cacheDuration in body should match '^([0-9]+(s|m|h))+$'"),
			Entry("ServeStaleDuration with decimal values", `{"spec":{"localDNS":{"vnetDNSOverrides":{"test.domain":{"serveStaleDuration":"5.5h"}}}}}`, "\"5.5h\": spec.localDNS.vnetDNSOverrides.test.domain.serveStaleDuration in body should match '^([0-9]+(s|m|h))+$'"),
			Entry("ServeStaleDuration with 'Never' value", `{"spec":{"localDNS":{"vnetDNSOverrides":{"test.domain":{"serveStaleDuration":"Never"}}}}}`, "\"Never\": spec.localDNS.vnetDNSOverrides.test.domain.serveStaleDuration in body should match '^([0-9]+(s|m|h))+$'"),
		)

		DescribeTable("should validate ServeStaleDuration", func(durationStr string, expectedErr string) {
			serveStaleDuration := karpv1.MustParseNillableDuration(durationStr)
			overrideConfig := createCompleteLocalDNSOverrides(false)
			overrideConfig.ServeStaleDuration = serveStaleDuration
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(true), "cluster.local": createCompleteLocalDNSOverrides(false), "test.domain": overrideConfig},
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{".": createCompleteLocalDNSOverrides(false), "cluster.local": createCompleteLocalDNSOverrides(false)},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid duration: 1h", "1h", ""),
			Entry("valid duration: 30m", "30m", ""),
			Entry("valid duration: 60s", "60s", ""),
			Entry("valid duration: 1h30m", "1h30m", ""),
			Entry("valid duration: 2h15m30s", "2h15m30s", ""),
		)

		DescribeTable("should validate required zones in VnetDNSOverrides", func(zones map[string]*v1beta1.LocalDNSOverrides, expectedErr string) {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: zones,
						KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
							".":             createCompleteLocalDNSOverrides(false),
							"cluster.local": createCompleteLocalDNSOverrides(false),
						},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid: both required zones present", map[string]*v1beta1.LocalDNSOverrides{
				".":             createCompleteLocalDNSOverrides(true), // Root zone must use VnetDNS
				"cluster.local": createCompleteLocalDNSOverrides(false),
			}, ""),
			Entry("valid: both required zones plus additional zone", map[string]*v1beta1.LocalDNSOverrides{
				".":             createCompleteLocalDNSOverrides(true), // Root zone must use VnetDNS
				"cluster.local": createCompleteLocalDNSOverrides(false),
				"example.com":   createCompleteLocalDNSOverrides(false),
			}, ""),
			Entry("invalid: missing root zone", map[string]*v1beta1.LocalDNSOverrides{
				"cluster.local": createCompleteLocalDNSOverrides(false),
			}, "vnetDNSOverrides must contain required zones '.' and 'cluster.local'"),
			Entry("invalid: missing cluster.local zone", map[string]*v1beta1.LocalDNSOverrides{
				".": createCompleteLocalDNSOverrides(true), // Root zone must use VnetDNS
			}, "vnetDNSOverrides must contain required zones '.' and 'cluster.local'"),
			Entry("invalid: missing both required zones", map[string]*v1beta1.LocalDNSOverrides{
				"example.com": createCompleteLocalDNSOverrides(false),
			}, "vnetDNSOverrides must contain required zones '.' and 'cluster.local'"),
		)

		DescribeTable("should validate required zones in KubeDNSOverrides", func(zones map[string]*v1beta1.LocalDNSOverrides, expectedErr string) {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
						VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
							".":             createCompleteLocalDNSOverrides(true), // Root zone must use VnetDNS
							"cluster.local": createCompleteLocalDNSOverrides(false),
						},
						KubeDNSOverrides: zones,
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			if expectedErr == "" {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			}
		},
			Entry("valid: both required zones present", map[string]*v1beta1.LocalDNSOverrides{
				".":             createCompleteLocalDNSOverrides(false),
				"cluster.local": createCompleteLocalDNSOverrides(false),
			}, ""),
			Entry("valid: both required zones plus additional zone", map[string]*v1beta1.LocalDNSOverrides{
				".":             createCompleteLocalDNSOverrides(false),
				"cluster.local": createCompleteLocalDNSOverrides(false),
				"example.com":   createCompleteLocalDNSOverrides(false),
			}, ""),
			Entry("invalid: missing root zone", map[string]*v1beta1.LocalDNSOverrides{
				"cluster.local": createCompleteLocalDNSOverrides(false),
			}, "kubeDNSOverrides must contain required zones '.' and 'cluster.local'"),
			Entry("invalid: missing cluster.local zone", map[string]*v1beta1.LocalDNSOverrides{
				".": createCompleteLocalDNSOverrides(false),
			}, "kubeDNSOverrides must contain required zones '.' and 'cluster.local'"),
			Entry("invalid: missing both required zones", map[string]*v1beta1.LocalDNSOverrides{
				"example.com": createCompleteLocalDNSOverrides(false),
			}, "kubeDNSOverrides must contain required zones '.' and 'cluster.local'"),
		)

		Context("DNS forwarding restrictions", func() {
			It("should reject forwarding root zone '.' to ClusterCoreDNS from vnetDNSOverrides", func() {
				rootOverride := createCompleteLocalDNSOverrides(true) // Root zone needs VnetDNS
				rootOverride.ForwardDestination = lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             rootOverride,
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("DNS traffic for root zone '.' cannot be forwarded to ClusterCoreDNS from vnetDNSOverrides"))
			})

			It("should allow forwarding root zone '.' to VnetDNS from vnetDNSOverrides", func() {
				rootOverride := createCompleteLocalDNSOverrides(true) // Root zone needs VnetDNS
				rootOverride.ForwardDestination = lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             rootOverride,
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			})

			It("should reject forwarding 'cluster.local' to VnetDNS from vnetDNSOverrides", func() {
				clusterLocalOverride := createCompleteLocalDNSOverrides(false)
				clusterLocalOverride.ForwardDestination = lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(true), // Root zone needs VnetDNS
								"cluster.local": clusterLocalOverride,
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("DNS traffic for 'cluster.local' cannot be forwarded to VnetDNS from vnetDNSOverrides"))
			})

			It("should reject forwarding 'cluster.local' to VnetDNS from kubeDNSOverrides", func() {
				clusterLocalOverride := createCompleteLocalDNSOverrides(false)
				clusterLocalOverride.ForwardDestination = lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(true), // Root zone needs VnetDNS
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": clusterLocalOverride,
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("DNS traffic for 'cluster.local' cannot be forwarded to VnetDNS from kubeDNSOverrides"))
			})

			It("should reject forwarding zones ending with 'cluster.local' to VnetDNS", func() {
				subZoneOverride := createCompleteLocalDNSOverrides(false)
				subZoneOverride.ForwardDestination = lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":                 createCompleteLocalDNSOverrides(true), // Root zone needs VnetDNS
								"cluster.local":     createCompleteLocalDNSOverrides(false),
								"sub.cluster.local": subZoneOverride,
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("DNS traffic for 'cluster.local' cannot be forwarded to VnetDNS"))
			})

			It("should allow forwarding 'cluster.local' to ClusterCoreDNS", func() {
				clusterLocalOverride := createCompleteLocalDNSOverrides(false)
				clusterLocalOverride.ForwardDestination = lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(true), // Root zone needs VnetDNS
								"cluster.local": clusterLocalOverride,
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			})
		})

		Context("ServeStale and Protocol validation", func() {
			It("should reject ServeStale Verify with ForceTCP protocol", func() {
				invalidOverride := createCompleteLocalDNSOverrides(true) // Root zone needs VnetDNS
				invalidOverride.ServeStale = lo.ToPtr(v1beta1.LocalDNSServeStaleVerify)
				invalidOverride.Protocol = lo.ToPtr(v1beta1.LocalDNSProtocolForceTCP)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             invalidOverride,
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ServeStale verify cannot be used with ForceTCP protocol"))
			})

			It("should allow ServeStale Immediate with ForceTCP protocol", func() {
				validOverride := createCompleteLocalDNSOverrides(true) // Root zone needs VnetDNS
				validOverride.ServeStale = lo.ToPtr(v1beta1.LocalDNSServeStaleImmediate)
				validOverride.Protocol = lo.ToPtr(v1beta1.LocalDNSProtocolForceTCP)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             validOverride,
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			})

			It("should allow ServeStale Verify with PreferUDP protocol", func() {
				validOverride := createCompleteLocalDNSOverrides(true) // Root zone needs VnetDNS
				validOverride.ServeStale = lo.ToPtr(v1beta1.LocalDNSServeStaleVerify)
				validOverride.Protocol = lo.ToPtr(v1beta1.LocalDNSProtocolPreferUDP)

				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
							VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             validOverride,
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
							KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
								".":             createCompleteLocalDNSOverrides(false),
								"cluster.local": createCompleteLocalDNSOverrides(false),
							},
						},
					},
				}
				Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			})
		})

		Context("Zone name format validation", func() {
			DescribeTable("should validate zone name format in VnetDNSOverrides",
				func(zoneName string, expectedErr string) {
					nodeClass := &v1beta1.AKSNodeClass{
						ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
						Spec: v1beta1.AKSNodeClassSpec{
							LocalDNS: &v1beta1.LocalDNS{
								Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
								VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
									".":             createCompleteLocalDNSOverrides(true),
									"cluster.local": createCompleteLocalDNSOverrides(false),
									zoneName:        createCompleteLocalDNSOverrides(false),
								},
								KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
									".":             createCompleteLocalDNSOverrides(false),
									"cluster.local": createCompleteLocalDNSOverrides(false),
								},
							},
						},
					}
					err := env.Client.Create(ctx, nodeClass)
					if expectedErr == "" {
						Expect(err).To(Succeed())
					} else {
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring(expectedErr))
					}
				},
				Entry("valid: simple domain", "example.com", ""),
				Entry("valid: subdomain", "sub.example.com", ""),
				Entry("valid: with trailing dot", "example.com.", ""),
				Entry("valid: with hyphens", "my-domain.example.com", ""),
				Entry("valid: with underscores", "my_domain.example.com", ""),
				Entry("valid: multiple levels", "a.b.c.d.example.com", ""),
				Entry("valid: numeric start", "123.example.com", ""),
				Entry("valid: mixed case", "MyDomain.Example.COM", ""),
				Entry("valid: single character label", "a.b", ""),
				Entry("valid: label with numbers only", "123.456.com", ""),
				Entry("valid: label max length 63", strings.Repeat("a", 63)+".example.com", ""),
				Entry("valid: mixed alphanumeric with hyphen and underscore", "a1-b2_c3.example.com", ""),
				Entry("valid: starting with number ending with letter", "1abc.example.com", ""),
				Entry("valid: starting with letter ending with number", "abc1.example.com", ""),
				Entry("valid: hyphen in middle", "my-zone.example.com", ""),
				Entry("valid: underscore in middle", "my_zone.example.com", ""),
				Entry("valid: mixed hyphens and underscores", "a-b_c-d.example.com", ""),
				Entry("valid: single label domain", "localhost", ""),
				Entry("valid: single label with number", "host123", ""),
				Entry("valid: numbers with trailing dot", "123.456.", ""),
				Entry("valid: complex mixed case with special chars", "A1-B_2.Example.COM", ""),
				Entry("invalid: starts with hyphen", "-invalid.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: ends with hyphen in label", "invalid-.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: starts with underscore", "_invalid.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: ends with underscore in label", "invalid_.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: double dots", "invalid..com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: starts with dot", ".invalid.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters @", "invalid@domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters #", "invalid#domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters $", "invalid$domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters %", "invalid%domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters &", "invalid&domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters *", "invalid*domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters space", "invalid domain.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: label too long 64 chars", strings.Repeat("a", 64)+".example.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: label too long 65 chars", strings.Repeat("a", 65)+".example.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: empty label between dots", "invalid..example.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: only hyphen", "-.example.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: only underscore", "_.example.com", "vnetDNSOverrides contains invalid zone name format"),
				Entry("invalid: ends with dot dot", "example.com..", "vnetDNSOverrides contains invalid zone name format"),
			)

			DescribeTable("should validate zone name format in KubeDNSOverrides",
				func(zoneName string, expectedErr string) {
					nodeClass := &v1beta1.AKSNodeClass{
						ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
						Spec: v1beta1.AKSNodeClassSpec{
							LocalDNS: &v1beta1.LocalDNS{
								Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
								VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
									".":             createCompleteLocalDNSOverrides(true), // Root zone must use VnetDNS
									"cluster.local": createCompleteLocalDNSOverrides(false),
								},
								KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
									".":             createCompleteLocalDNSOverrides(false),
									"cluster.local": createCompleteLocalDNSOverrides(false),
									zoneName:        createCompleteLocalDNSOverrides(false),
								},
							},
						},
					}
					err := env.Client.Create(ctx, nodeClass)
					if expectedErr == "" {
						Expect(err).To(Succeed())
					} else {
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring(expectedErr))
					}
				},
				Entry("valid: simple domain", "example.com", ""),
				Entry("valid: subdomain", "sub.example.com", ""),
				Entry("valid: with trailing dot", "example.com.", ""),
				Entry("valid: with hyphens", "my-domain.example.com", ""),
				Entry("valid: with underscores", "my_domain.example.com", ""),
				Entry("valid: single character label", "a.b", ""),
				Entry("valid: label with numbers only", "123.456.com", ""),
				Entry("valid: label max length 63", strings.Repeat("a", 63)+".example.com", ""),
				Entry("valid: mixed alphanumeric", "a1b2c3.example.com", ""),
				Entry("valid: single label domain", "localhost", ""),
				Entry("valid: single label with number", "host123", ""),
				Entry("invalid: starts with hyphen", "-invalid.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: ends with hyphen", "invalid-.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: starts with underscore", "_invalid.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: ends with underscore", "invalid_.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: double dots", "invalid..com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: starts with dot", ".invalid.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters @", "invalid@domain.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters #", "invalid#domain.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: special characters space", "invalid domain.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: label too long 64 chars", strings.Repeat("a", 64)+".example.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: only hyphen", "-.example.com", "kubeDNSOverrides contains invalid zone name format"),
				Entry("invalid: only underscore", "_.example.com", "kubeDNSOverrides contains invalid zone name format"),
			)
		})

	})

	Context("OSDiskSizeGB", func() {
		DescribeTable("Should validate OSDiskSizeGB constraints", func(osDiskSizeGB *int32, expected bool) {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
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
		DescribeTable("should only accept valid ImageFamily and FIPSMode combinations", func(imageFamily string, fipsMode *v1beta1.FIPSMode, expected bool) {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec:       v1beta1.AKSNodeClassSpec{},
			}
			// allows for leaving imageFamily unset, which currently defaults to Ubuntu2204
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
			Entry("generic Ubuntu when FIPSMode is explicitly Disabled should succeed", v1beta1.UbuntuImageFamily, &v1beta1.FIPSModeDisabled, true),
			Entry("generic Ubuntu when FIPSMode is not explicitly set should succeed", v1beta1.UbuntuImageFamily, nil, true),
			Entry("generic Ubuntu when FIPSMode is explicitly FIPS should succeed", v1beta1.UbuntuImageFamily, &v1beta1.FIPSModeFIPS, true),
			Entry("Ubuntu2204 when FIPSMode is explicitly Disabled should succeed", v1beta1.Ubuntu2204ImageFamily, &v1beta1.FIPSModeDisabled, true),
			Entry("Ubuntu2204 when FIPSMode is not explicitly set should succeed", v1beta1.Ubuntu2204ImageFamily, nil, true),
			//TODO: Modify when Ubuntu 22.04 with FIPS becomes available
			Entry("Ubuntu2204 when FIPSMode is explicitly FIPS should fail", v1beta1.Ubuntu2204ImageFamily, &v1beta1.FIPSModeFIPS, false),
			Entry("Ubuntu2404 when FIPSMode is explicitly Disabled should succeed", v1beta1.Ubuntu2404ImageFamily, &v1beta1.FIPSModeDisabled, true),
			Entry("Ubuntu2404 when FIPSMode is not explicitly set should succeed", v1beta1.Ubuntu2404ImageFamily, nil, true),
			//TODO: Modify when Ubuntu 24.04 with FIPS becomes available
			Entry("Ubuntu2404 when FIPSMode is explicitly FIPS should fail", v1beta1.Ubuntu2404ImageFamily, &v1beta1.FIPSModeFIPS, false),
			Entry("generic AzureLinux when FIPSMode is explicitly Disabled should succeed", v1beta1.AzureLinuxImageFamily, &v1beta1.FIPSModeDisabled, true),
			Entry("generic AzureLinux when FIPSMode is not explicitly set should succeed", v1beta1.AzureLinuxImageFamily, nil, true),
			Entry("generic AzureLinux when FIPSMode is explicitly FIPS should succeed", v1beta1.AzureLinuxImageFamily, &v1beta1.FIPSModeFIPS, true),
			Entry("unspecified ImageFamily (defaults to Ubuntu) when FIPSMode is explicitly Disabled should succeed", "", &v1beta1.FIPSModeDisabled, true),
			Entry("unspecified ImageFamily (defaults to Ubuntu) when FIPSMode is not explicitly set should succeed", "", nil, true),
			Entry("unspecified ImageFamily (defaults to Ubuntu) when FIPSMode is explicitly FIPS should succeed", "", &v1beta1.FIPSModeFIPS, true),
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
		It("should not allow internal labels", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1beta1.RestrictedLabels {
				nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{NodeSelectorRequirement: corev1.NodeSelectorRequirement{Key: label, Operator: corev1.NodeSelectorOpIn, Values: []string{"test"}}},
				}
				Expect(env.Client.Create(ctx, nodePool)).ToNot(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
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
		It("should not allow internal labels", func() {
			oldNodePool := nodePool.DeepCopy()
			for label := range v1beta1.RestrictedLabels {
				nodePool.Spec.Template.Labels = map[string]string{
					label: "test",
				}
				Expect(env.Client.Create(ctx, nodePool)).ToNot(Succeed())
				nodePool = oldNodePool.DeepCopy()
			}
		})
	})

	Context("Tags", func() {
		It("should allow tags with valid keys and values", func() {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
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
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
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
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
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
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					Tags: map[string]string{
						strings.Repeat("a", 512): strings.Repeat("b", 256),
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})
	})
})
