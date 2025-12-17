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

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = AfterEach(func() {
	// Clean up all AKSNodeClasses created during tests
	nodeClassList := &v1beta1.AKSNodeClassList{}
	if err := env.Client.List(ctx, nodeClassList); err == nil {
		for i := range nodeClassList.Items {
			_ = env.Client.Delete(ctx, &nodeClassList.Items[i])
		}
	}
})

var _ = Describe("CEL/Validation", func() {
	var nodePool *karpv1.NodePool

	// Helper function to create a complete LocalDNSZoneOverride with all required fields
	// Use forwardToVnetDNS=true for root zone "." in vnetDNSOverrides
	createCompleteLocalDNSZoneOverride := func(zone string, forwardToVnetDNS bool) v1beta1.LocalDNSZoneOverride {
		forwardDest := v1beta1.LocalDNSForwardDestinationClusterCoreDNS
		if forwardToVnetDNS {
			forwardDest = v1beta1.LocalDNSForwardDestinationVnetDNS
		}
		return v1beta1.LocalDNSZoneOverride{
			Zone:               zone,
			QueryLogging:       v1beta1.LocalDNSQueryLoggingError,
			Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
			ForwardDestination: forwardDest,
			ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
			MaxConcurrent:      lo.ToPtr(int32(100)),
			CacheDuration:      karpv1.MustParseNillableDuration("1h"),
			ServeStaleDuration: karpv1.MustParseNillableDuration("30m"),
			ServeStale:         v1beta1.LocalDNSServeStaleVerify,
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
						Mode: v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", true),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
						},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", false),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
						},
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
		})

		DescribeTable("should validate LocalDNSMode", func(mode v1beta1.LocalDNSMode, expectedErr string) {
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             mode,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false)},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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
			Entry("valid mode: Preferred", v1beta1.LocalDNSModePreferred, ""),
			Entry("valid mode: Required", v1beta1.LocalDNSModeRequired, ""),
			Entry("valid mode: Disabled", v1beta1.LocalDNSModeDisabled, ""),
			Entry("invalid mode: invalid-string", v1beta1.LocalDNSMode("invalid-string"), "spec.localDNS.mode"),
			Entry("invalid mode: empty", v1beta1.LocalDNSMode(""), "spec.localDNS.mode"),
		)

		DescribeTable("should validate LocalDNSQueryLogging", func(queryLogging v1beta1.LocalDNSQueryLogging, expectedErr string) {
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.QueryLogging = queryLogging
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode: v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", true),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
							overrideConfig,
						},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", false),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
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
			Entry("valid query logging: Error", v1beta1.LocalDNSQueryLoggingError, ""),
			Entry("valid query logging: Log", v1beta1.LocalDNSQueryLoggingLog, ""),
			Entry("invalid query logging: invalid-string", v1beta1.LocalDNSQueryLogging("invalid-string"), "queryLogging"),
			Entry("invalid query logging: empty", v1beta1.LocalDNSQueryLogging(""), "queryLogging"),
		)

		DescribeTable("should validate LocalDNSProtocol", func(protocol v1beta1.LocalDNSProtocol, expectedErr string) {
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.Protocol = protocol
			// When using ForceTCP, we can't use ServeStaleVerify, so use Immediate instead
			if protocol == v1beta1.LocalDNSProtocolForceTCP {
				overrideConfig.ServeStale = v1beta1.LocalDNSServeStaleImmediate
			}
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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
			Entry("valid protocol: PreferUDP", v1beta1.LocalDNSProtocolPreferUDP, ""),
			Entry("valid protocol: ForceTCP", v1beta1.LocalDNSProtocolForceTCP, ""),
			Entry("invalid protocol: invalid-string", v1beta1.LocalDNSProtocol("invalid-string"), "protocol"),
			Entry("invalid protocol: empty", v1beta1.LocalDNSProtocol(""), "protocol"),
		)

		DescribeTable("should validate LocalDNSForwardDestination", func(forwardDestination v1beta1.LocalDNSForwardDestination, expectedErr string) {
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.ForwardDestination = forwardDestination
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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
			Entry("valid forward destination: ClusterCoreDNS", v1beta1.LocalDNSForwardDestinationClusterCoreDNS, ""),
			Entry("valid forward destination: VnetDNS", v1beta1.LocalDNSForwardDestinationVnetDNS, ""),
			Entry("invalid forward destination: invalid-string", v1beta1.LocalDNSForwardDestination("invalid-string"), "forwardDestination"),
			Entry("invalid forward destination: empty", v1beta1.LocalDNSForwardDestination(""), "forwardDestination"),
		)

		DescribeTable("should validate LocalDNSForwardPolicy", func(forwardPolicy v1beta1.LocalDNSForwardPolicy, expectedErr string) {
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.ForwardPolicy = forwardPolicy
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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
			Entry("valid forward policy: Sequential", v1beta1.LocalDNSForwardPolicySequential, ""),
			Entry("valid forward policy: RoundRobin", v1beta1.LocalDNSForwardPolicyRoundRobin, ""),
			Entry("valid forward policy: Random", v1beta1.LocalDNSForwardPolicyRandom, ""),
			Entry("invalid forward policy: invalid-string", v1beta1.LocalDNSForwardPolicy("invalid-string"), "forwardPolicy"),
			Entry("invalid forward policy: empty", v1beta1.LocalDNSForwardPolicy(""), "forwardPolicy"),
		)

		DescribeTable("should validate LocalDNSServeStale", func(serveStale v1beta1.LocalDNSServeStale, expectedErr string) {
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.ServeStale = serveStale
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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
			Entry("valid serve stale: Verify", v1beta1.LocalDNSServeStaleVerify, ""),
			Entry("valid serve stale: Immediate", v1beta1.LocalDNSServeStaleImmediate, ""),
			Entry("valid serve stale: Disable", v1beta1.LocalDNSServeStaleDisable, ""),
			Entry("invalid serve stale: invalid-string", v1beta1.LocalDNSServeStale("invalid-string"), "serveStale"),
			Entry("invalid serve stale: empty", v1beta1.LocalDNSServeStale(""), "serveStale"),
		)

		DescribeTable("should validate CacheDuration", func(durationStr string, expectedErr string) {
			cacheDuration := karpv1.MustParseNillableDuration(durationStr)
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.CacheDuration = cacheDuration
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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

		DescribeTable("should validate ServeStaleDuration", func(durationStr string, expectedErr string) {
			serveStaleDuration := karpv1.MustParseNillableDuration(durationStr)
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.ServeStaleDuration = serveStaleDuration
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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

		DescribeTable("should validate MaxConcurrent", func(maxConcurrent *int32, expectedErr string) {
			overrideConfig := createCompleteLocalDNSZoneOverride("test.domain", false)
			overrideConfig.MaxConcurrent = maxConcurrent
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode:             v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false), overrideConfig},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
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
			Entry("valid: 0 (minimum)", lo.ToPtr(int32(0)), ""),
			Entry("valid: 1", lo.ToPtr(int32(1)), ""),
			Entry("valid: 100", lo.ToPtr(int32(100)), ""),
			Entry("valid: 1000", lo.ToPtr(int32(1000)), ""),
			Entry("invalid: -1 (below minimum)", lo.ToPtr(int32(-1)), "maxConcurrent"),
			Entry("invalid: -100 (below minimum)", lo.ToPtr(int32(-100)), "maxConcurrent"),
		)

		It("should reject duplicate zones in VnetDNSOverrides due to listType=map", func() {
			// This test proves that listType=map with listMapKey=zone enforces uniqueness
			// at the API server level, making explicit CEL duplicate validation redundant
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode: v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", true),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
							createCompleteLocalDNSZoneOverride("example.com", false),
							createCompleteLocalDNSZoneOverride("example.com", false), // Duplicate zone
						},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", false),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
						},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			// The API server rejects this due to listType=map enforcement
			Expect(err.Error()).To(ContainSubstring("Duplicate value"))
			Expect(err.Error()).To(ContainSubstring("{\"zone\":\"example.com\"}"))
		})

		It("should reject duplicate zones in KubeDNSOverrides due to listType=map", func() {
			// This test proves that listType=map with listMapKey=zone enforces uniqueness
			// at the API server level, making explicit CEL duplicate validation redundant
			nodeClass := &v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
				Spec: v1beta1.AKSNodeClassSpec{
					LocalDNS: &v1beta1.LocalDNS{
						Mode: v1beta1.LocalDNSModeRequired,
						VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", true),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
						},
						KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
							createCompleteLocalDNSZoneOverride(".", false),
							createCompleteLocalDNSZoneOverride("cluster.local", false),
							createCompleteLocalDNSZoneOverride("test.com", false),
							createCompleteLocalDNSZoneOverride("test.com", false), // Duplicate zone
						},
					},
				},
			}
			err := env.Client.Create(ctx, nodeClass)
			Expect(err).To(HaveOccurred())
			// The API server rejects this due to listType=map enforcement
			Expect(err.Error()).To(ContainSubstring("Duplicate value"))
			Expect(err.Error()).To(ContainSubstring("{\"zone\":\"test.com\"}"))
		})

		DescribeTable("should validate required zones in overrides",
			func(vnetOverrides []v1beta1.LocalDNSZoneOverride, kubeOverrides []v1beta1.LocalDNSZoneOverride, expectedErr string) {
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode:             v1beta1.LocalDNSModeRequired,
							VnetDNSOverrides: vnetOverrides,
							KubeDNSOverrides: kubeOverrides,
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			},
			Entry("VnetDNSOverrides missing root zone '.'",
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride("cluster.local", false)},
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
				"must contain required zones '.' and 'cluster.local'"),
			Entry("VnetDNSOverrides missing 'cluster.local'",
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true)},
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false), createCompleteLocalDNSZoneOverride("cluster.local", false)},
				"must contain required zones '.' and 'cluster.local'"),
			Entry("KubeDNSOverrides missing root zone '.'",
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false)},
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride("cluster.local", false)},
				"must contain required zones '.' and 'cluster.local'"),
			Entry("KubeDNSOverrides missing 'cluster.local'",
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", true), createCompleteLocalDNSZoneOverride("cluster.local", false)},
				[]v1beta1.LocalDNSZoneOverride{createCompleteLocalDNSZoneOverride(".", false)},
				"must contain required zones '.' and 'cluster.local'"),
		)

		DescribeTable("should validate zone forwarding restrictions",
			func(testZone string, forwardDest v1beta1.LocalDNSForwardDestination, expectedErr string) {
				override := createCompleteLocalDNSZoneOverride(testZone, false)
				override.ForwardDestination = forwardDest
				vnetOverrides := []v1beta1.LocalDNSZoneOverride{
					createCompleteLocalDNSZoneOverride(".", true),
					createCompleteLocalDNSZoneOverride("cluster.local", false),
				}
				// Replace the appropriate zone in vnetOverrides
				if testZone == "." {
					vnetOverrides[0] = override
				} else {
					vnetOverrides = append(vnetOverrides, override)
				}
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode:             v1beta1.LocalDNSModeRequired,
							VnetDNSOverrides: vnetOverrides,
							KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
								createCompleteLocalDNSZoneOverride(".", false),
								createCompleteLocalDNSZoneOverride("cluster.local", false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedErr))
			},
			Entry("root zone '.' cannot be forwarded to ClusterCoreDNS in VnetDNSOverrides",
				".", v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
				"root zone '.' cannot be forwarded to ClusterCoreDNS from vnetDNSOverrides"),
			Entry("'cluster.local' cannot be forwarded to VnetDNS",
				"cluster.local", v1beta1.LocalDNSForwardDestinationVnetDNS,
				"'cluster.local' cannot be forwarded to VnetDNS"),
			Entry("subdomain of 'cluster.local' cannot be forwarded to VnetDNS",
				"svc.cluster.local", v1beta1.LocalDNSForwardDestinationVnetDNS,
				"'cluster.local' cannot be forwarded to VnetDNS"),
		)

		DescribeTable("should validate protocol and serveStale combinations",
			func(protocol v1beta1.LocalDNSProtocol, serveStale v1beta1.LocalDNSServeStale, shouldSucceed bool) {
				override := createCompleteLocalDNSZoneOverride("example.com", false)
				override.Protocol = protocol
				override.ServeStale = serveStale
				nodeClass := &v1beta1.AKSNodeClass{
					ObjectMeta: metav1.ObjectMeta{Name: strings.ToLower(randomdata.SillyName())},
					Spec: v1beta1.AKSNodeClassSpec{
						LocalDNS: &v1beta1.LocalDNS{
							Mode: v1beta1.LocalDNSModeRequired,
							VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
								createCompleteLocalDNSZoneOverride(".", true),
								createCompleteLocalDNSZoneOverride("cluster.local", false),
								override,
							},
							KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
								createCompleteLocalDNSZoneOverride(".", false),
								createCompleteLocalDNSZoneOverride("cluster.local", false),
							},
						},
					},
				}
				err := env.Client.Create(ctx, nodeClass)
				if shouldSucceed {
					Expect(err).To(Succeed())
				} else {
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("serveStale Verify cannot be used with protocol ForceTCP"))
				}
			},
			Entry("reject: ForceTCP with Verify", v1beta1.LocalDNSProtocolForceTCP, v1beta1.LocalDNSServeStaleVerify, false),
			Entry("accept: ForceTCP with Immediate", v1beta1.LocalDNSProtocolForceTCP, v1beta1.LocalDNSServeStaleImmediate, true),
			Entry("accept: ForceTCP with Disable", v1beta1.LocalDNSProtocolForceTCP, v1beta1.LocalDNSServeStaleDisable, true),
			Entry("accept: PreferUDP with Verify", v1beta1.LocalDNSProtocolPreferUDP, v1beta1.LocalDNSServeStaleVerify, true),
		)
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
