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
	"time"

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hash", func() {
	// NOTE: When the hashing algorithm is updated, these tests are expected to fail; test hash constants here would have to be updated, and currentHashVersion would have to be updated to the new version matching v1beta1.AKSNodeClassHashVersion
	const staticHash = "4108492229247269128"
	var nodeClass *v1beta1.AKSNodeClass
	BeforeEach(func() {
		nodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1beta1.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            lo.ToPtr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       lo.ToPtr("none"),
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         lo.ToPtr("10Mi"),
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
		}
	})
	DescribeTable(
		"should match static hash on field value change",
		func(hash string, changes v1beta1.AKSNodeClass) {
			Expect(mergo.Merge(nodeClass, changes, mergo.WithOverride, mergo.WithSliceDeepCopy)).To(Succeed())
			Expect(nodeClass.Hash()).To(Equal(hash))
		},
		Entry("Base AKSNodeClass", staticHash, v1beta1.AKSNodeClass{}),

		// Static fields, expect changed hash from base
		Entry("VNETSubnetID", "13971920214979852468", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}),
		Entry("WorkloadRuntime", "13352508654154828139", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{WorkloadRuntime: lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)}}),
		Entry("OSDiskType", "10944184826674581697", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskType: lo.ToPtr(v1beta1.OSDiskTypeManaged)}}),
		Entry("OSDiskSizeGB", "7816855636861645563", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}),
		Entry("ImageFamily", "15616969746300892810", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}),
		Entry("Kubelet", "33638514539106194", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{Kubelet: &v1beta1.KubeletConfiguration{CPUManagerPolicy: lo.ToPtr("none")}}}),
		Entry("MaxPods", "15508761509963240710", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}),
		Entry("LocalDNS.Mode", "17805442572569734619", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}}}),
		Entry("LocalDNS.VnetDNSOverrides", "14608914734386108436", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", QueryLogging: v1beta1.LocalDNSQueryLoggingLog}}}}}),
		Entry("LocalDNS.KubeDNSOverrides", "4529827108104295737", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1beta1.LocalDNSProtocolForceTCP}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.CacheDuration", "11008649797056761238", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", CacheDuration: karpv1.MustParseNillableDuration("1h")}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.ServeStaleDuration", "4895720480850206885", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", ServeStaleDuration: karpv1.MustParseNillableDuration("30m")}}}}}),
		Entry("ArtifactStreaming.Enabled", "15355387647114481444", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ArtifactStreaming: &v1beta1.ArtifactStreaming{Enabled: lo.ToPtr(true)}}}),
		// A disabled nodePublicIP block must hash the same as the pre-feature base, so writing it
		// doesn't drift existing nodes.
		Entry("NodePublicIP empty pre-feature compatibility", staticHash, v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{NodePublicIP: &v1beta1.NodePublicIP{}}}),
		Entry("NodePublicIP.Enabled false pre-feature compatibility", staticHash, v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{NodePublicIP: &v1beta1.NodePublicIP{Enabled: lo.ToPtr(false)}}}),
	)

	DescribeTable("should change hash when static fields are updated", func(changes v1beta1.AKSNodeClass) {
		hash := nodeClass.Hash()
		Expect(mergo.Merge(nodeClass, changes, mergo.WithOverride, mergo.WithSliceDeepCopy)).To(Succeed())
		updatedHash := nodeClass.Hash()
		Expect(hash).ToNot(Equal(updatedHash))
	},
		Entry("VNETSubnetID", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}),
		Entry("OSDiskType", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskType: lo.ToPtr(v1beta1.OSDiskTypeManaged)}}),
		Entry("OSDiskSizeGB", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}),
		Entry("ImageFamily", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}),
		Entry("Kubelet", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{Kubelet: &v1beta1.KubeletConfiguration{CPUManagerPolicy: lo.ToPtr("none")}}}),
		Entry("MaxPods", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}),
		Entry("CapacityReservation", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{CapacityReservation: &v1beta1.CapacityReservationConfiguration{GroupID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Compute/capacityReservationGroups/crg")}}}),
		Entry("LocalDNS.Mode", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}}}),
		Entry("LocalDNS.VnetDNSOverrides", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", QueryLogging: v1beta1.LocalDNSQueryLoggingLog}}}}}),
		Entry("LocalDNS.KubeDNSOverrides", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1beta1.LocalDNSProtocolForceTCP}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.CacheDuration", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", CacheDuration: karpv1.MustParseNillableDuration("2h")}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.ServeStaleDuration", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{LocalDNS: &v1beta1.LocalDNS{VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", ServeStaleDuration: karpv1.MustParseNillableDuration("1h")}}}}}),
		Entry("ArtifactStreaming.Enabled", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{ArtifactStreaming: &v1beta1.ArtifactStreaming{Enabled: lo.ToPtr(true)}}}),
		Entry("NodePublicIP.Enabled", v1beta1.AKSNodeClass{Spec: v1beta1.AKSNodeClassSpec{NodePublicIP: &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}}}),
	)
	Context("NodePublicIP", func() {
		const (
			prefixID      = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Network/publicIPPrefixes/prefix"
			otherPrefixID = "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Network/publicIPPrefixes/other"
		)
		var (
			routingPreferenceTag = v1beta1.IPTag{IPTagType: "RoutingPreference", Tag: "Internet"}
			firstPartyUsageTag   = v1beta1.IPTag{IPTagType: "FirstPartyUsage", Tag: "/Unprivileged"}
		)

		It("should hash an empty prefix list the same as an omitted one", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
			hash := nodeClass.Hash()

			nodeClass.Spec.NodePublicIP.PrefixIDs = []string{}
			Expect(nodeClass.Hash()).To(Equal(hash))
		})
		It("should change hash when the prefix is added, changed, or removed", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
			noPrefixHash := nodeClass.Hash()

			nodeClass.Spec.NodePublicIP.PrefixIDs = []string{prefixID}
			prefixHash := nodeClass.Hash()
			Expect(prefixHash).ToNot(Equal(noPrefixHash))

			nodeClass.Spec.NodePublicIP.PrefixIDs = []string{otherPrefixID}
			Expect(nodeClass.Hash()).ToNot(Equal(prefixHash))
			Expect(nodeClass.Hash()).ToNot(Equal(noPrefixHash))

			nodeClass.Spec.NodePublicIP.PrefixIDs = nil
			Expect(nodeClass.Hash()).To(Equal(noPrefixHash))
		})
		It("should not change hash when prefix ID casing changes", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{prefixID}}
			hash := nodeClass.Hash()

			nodeClass.Spec.NodePublicIP.PrefixIDs = []string{strings.ToUpper(prefixID)}
			Expect(nodeClass.Hash()).To(Equal(hash))
		})
		It("should hash an empty IP tag list the same as an omitted one", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
			hash := nodeClass.Hash()

			nodeClass.Spec.NodePublicIP.IPTags = []v1beta1.IPTag{}
			Expect(nodeClass.Hash()).To(Equal(hash))
		})
		It("should not change hash when IP tags are reordered", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), IPTags: []v1beta1.IPTag{routingPreferenceTag, firstPartyUsageTag}}
			hash := nodeClass.Hash()

			nodeClass.Spec.NodePublicIP.IPTags = []v1beta1.IPTag{firstPartyUsageTag, routingPreferenceTag}
			Expect(nodeClass.Hash()).To(Equal(hash))
		})
		It("should change hash when an IP tag is added, changed, or removed", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true)}
			noTagsHash := nodeClass.Hash()

			nodeClass.Spec.NodePublicIP.IPTags = []v1beta1.IPTag{routingPreferenceTag}
			oneTagHash := nodeClass.Hash()
			Expect(oneTagHash).ToNot(Equal(noTagsHash))

			nodeClass.Spec.NodePublicIP.IPTags = []v1beta1.IPTag{routingPreferenceTag, firstPartyUsageTag}
			twoTagsHash := nodeClass.Hash()
			Expect(twoTagsHash).ToNot(Equal(oneTagHash))
			Expect(twoTagsHash).ToNot(Equal(noTagsHash))

			nodeClass.Spec.NodePublicIP.IPTags = []v1beta1.IPTag{{IPTagType: "RoutingPreference", Tag: "MicrosoftNetwork"}}
			Expect(nodeClass.Hash()).ToNot(Equal(oneTagHash))
			Expect(nodeClass.Hash()).ToNot(Equal(noTagsHash))

			// Tag values aren't normalized, so a casing change is a change.
			nodeClass.Spec.NodePublicIP.IPTags = []v1beta1.IPTag{{IPTagType: "RoutingPreference", Tag: "internet"}}
			Expect(nodeClass.Hash()).ToNot(Equal(oneTagHash))

			nodeClass.Spec.NodePublicIP.IPTags = nil
			Expect(nodeClass.Hash()).To(Equal(noTagsHash))
		})
		It("should not mutate node public IP settings while hashing", func() {
			nodeClass.Spec.NodePublicIP = &v1beta1.NodePublicIP{Enabled: lo.ToPtr(true), PrefixIDs: []string{strings.ToUpper(prefixID)}, IPTags: []v1beta1.IPTag{}}
			original := nodeClass.DeepCopy()

			_ = nodeClass.Hash()
			Expect(nodeClass).To(Equal(original))

			nodeClass.Spec.NodePublicIP.Enabled = lo.ToPtr(false)
			original = nodeClass.DeepCopy()

			_ = nodeClass.Hash()
			Expect(nodeClass).To(Equal(original))
		})
	})
	It("should not change hash when tags are re-ordered", func() {
		hash := nodeClass.Hash()
		nodeClass.Spec.Tags = map[string]string{"keyTag-2": "valueTag-2", "keyTag-1": "valueTag-1"}
		updatedHash := nodeClass.Hash()
		Expect(hash).To(Equal(updatedHash))
	})
	It("should not change hash when tags are changed", func() {
		hash := nodeClass.Hash()
		nodeClass.Spec.Tags = map[string]string{"keyTag-3": "valueTag-3"}
		updatedHash := nodeClass.Hash()
		Expect(hash).To(Equal(updatedHash))
	})
	It("should not change hash when capacity reservation group ID casing changes", func() {
		nodeClass.Spec.CapacityReservation = &v1beta1.CapacityReservationConfiguration{GroupID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/rg/providers/Microsoft.Compute/capacityReservationGroups/crg")}
		hash := nodeClass.Hash()
		nodeClass.Spec.CapacityReservation.GroupID = lo.ToPtr("/SUBSCRIPTIONS/12345678-1234-1234-1234-123456789012/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.COMPUTE/CAPACITYRESERVATIONGROUPS/CRG")
		Expect(nodeClass.Hash()).To(Equal(hash))
	})
	It("should expect two AKSNodeClasses with the same spec to have the same hash", func() {
		otherNodeClass := &v1beta1.AKSNodeClass{
			Spec: nodeClass.Spec,
		}
		Expect(nodeClass.Hash()).To(Equal(otherNodeClass.Hash()))
	})
	// workloadRuntime OCIContainer is the default and means "no additional runtime", so it must hash
	// the same as the field being absent. This is what lets the field carry a server-side default
	// without bumping AKSNodeClassHashVersion: the default landing on an existing AKSNodeClass, or a
	// user explicitly writing it, must not drift nodes. Kata must still drift them.
	It("should not change hash when workloadRuntime is explicitly set to the OCIContainer default", func() {
		Expect(nodeClass.Spec.WorkloadRuntime).To(BeNil())
		hash := nodeClass.Hash()

		nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeOCIContainer)
		Expect(nodeClass.Hash()).To(Equal(hash))
	})
	It("should change hash when workloadRuntime is KataVmIsolation", func() {
		hash := nodeClass.Hash()

		nodeClass.Spec.WorkloadRuntime = lo.ToPtr(v1beta1.WorkloadRuntimeKataVMIsolation)
		Expect(nodeClass.Hash()).ToNot(Equal(hash))
	})
	// This test is a sanity check to update the hashing version if the algorithm has been updated.
	// Note: this will only catch a missing version update, if the staticHash hasn't been updated yet.
	It("when hashing algorithm updates, we should update the hash version", func() {
		currentHashVersion := "v3"
		if nodeClass.Hash() != staticHash {
			Expect(v1beta1.AKSNodeClassHashVersion).ToNot(Equal(currentHashVersion))
		} else {
			// Note: this failure case is to ensure you have updated currentHashVersion, not AKSNodeClassHashVersion
			Expect(currentHashVersion).To(Equal(v1beta1.AKSNodeClassHashVersion))
		}
		// Note: this failure case is to ensure you have updated staticHash value
		Expect(staticHash).To(Equal(nodeClass.Hash()))
	})
})
