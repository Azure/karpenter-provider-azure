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
	"time"

	"dario.cat/mergo"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hash", func() {
	// NOTE: When the hashing algorithm is updated, these tests are expected to fail; test hash constants here would have to be updated, and currentHashVersion would have to be updated to the new version matching v1alpha2.AKSNodeClassHashVersion
	const staticHash = "4108492229247269128"
	var nodeClass *v1alpha2.AKSNodeClass
	BeforeEach(func() {
		nodeClass = &v1alpha2.AKSNodeClass{
			ObjectMeta: test.ObjectMeta(metav1.ObjectMeta{}),
			Spec: v1alpha2.AKSNodeClassSpec{
				VNETSubnetID: lo.ToPtr("subnet-id"),
				OSDiskSizeGB: lo.ToPtr(int32(30)),
				ImageFamily:  lo.ToPtr("Ubuntu2204"),
				Tags: map[string]string{
					"keyTag-1": "valueTag-1",
					"keyTag-2": "valueTag-2",
				},
				Kubelet: &v1alpha2.KubeletConfiguration{
					CPUManagerPolicy:            "static",
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{Duration: lo.Must(time.ParseDuration("100ms"))},
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					TopologyManagerPolicy:       "none",
					AllowedUnsafeSysctls:        []string{"net.core.somaxconn"},
					ContainerLogMaxSize:         "10Mi",
					ContainerLogMaxFiles:        lo.ToPtr(int32(10)),
				},
				MaxPods: lo.ToPtr(int32(100)),
			},
		}
	})
	DescribeTable(
		"should match static hash on field value change",
		func(hash string, changes v1alpha2.AKSNodeClass) {
			Expect(mergo.Merge(nodeClass, changes, mergo.WithOverride, mergo.WithSliceDeepCopy)).To(Succeed())
			Expect(nodeClass.Hash()).To(Equal(hash))
		},
		Entry("Base AKSNodeClass", staticHash, v1alpha2.AKSNodeClass{}),

		// Static fields, expect changed hash from base
		Entry("VNETSubnetID", "13971920214979852468", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}),
		Entry("OSDiskSizeGB", "7816855636861645563", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}),
		Entry("ImageFamily", "15616969746300892810", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}),
		Entry("Kubelet", "33638514539106194", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{Kubelet: &v1alpha2.KubeletConfiguration{CPUManagerPolicy: "none"}}}),
		Entry("MaxPods", "15508761509963240710", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}),
		Entry("LocalDNS.Mode", "17805442572569734619", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{Mode: v1alpha2.LocalDNSModeRequired}}}),
		Entry("LocalDNS.VnetDNSOverrides", "1846993897639680515", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {QueryLogging: lo.ToPtr(v1alpha2.LocalDNSQueryLoggingLog)}}}}}),
		Entry("LocalDNS.KubeDNSOverrides", "1649149597266483112", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{KubeDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {Protocol: lo.ToPtr(v1alpha2.LocalDNSProtocolForceTCP)}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.CacheDuration", "11176862737221572952", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {CacheDuration: karpv1.MustParseNillableDuration("1h")}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.ServeStaleDuration", "10694595590893870496", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {ServeStaleDuration: karpv1.MustParseNillableDuration("30m")}}}}}),
	)
	It("should match static hash when reordering tags", func() {
		nodeClass.Spec.Tags = map[string]string{"keyTag-2": "valueTag-2", "keyTag-1": "valueTag-1"}
		Expect(nodeClass.Hash()).To(Equal(staticHash))
	})
	DescribeTable("should change hash when static fields are updated", func(changes v1alpha2.AKSNodeClass) {
		hash := nodeClass.Hash()
		Expect(mergo.Merge(nodeClass, changes, mergo.WithOverride, mergo.WithSliceDeepCopy)).To(Succeed())
		updatedHash := nodeClass.Hash()
		Expect(hash).ToNot(Equal(updatedHash))
	},
		Entry("VNETSubnetID", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{VNETSubnetID: lo.ToPtr("subnet-id-2")}}),
		Entry("OSDiskSizeGB", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{OSDiskSizeGB: lo.ToPtr(int32(40))}}),
		Entry("ImageFamily", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{ImageFamily: lo.ToPtr("AzureLinux")}}),
		Entry("Kubelet", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{Kubelet: &v1alpha2.KubeletConfiguration{CPUManagerPolicy: "none"}}}),
		Entry("MaxPods", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{MaxPods: lo.ToPtr(int32(200))}}),
		Entry("LocalDNS.Mode", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{Mode: v1alpha2.LocalDNSModeRequired}}}),
		Entry("LocalDNS.VnetDNSOverrides", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {QueryLogging: lo.ToPtr(v1alpha2.LocalDNSQueryLoggingLog)}}}}}),
		Entry("LocalDNS.KubeDNSOverrides", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{KubeDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {Protocol: lo.ToPtr(v1alpha2.LocalDNSProtocolForceTCP)}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.CacheDuration", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {CacheDuration: karpv1.MustParseNillableDuration("2h")}}}}}),
		Entry("LocalDNS.VnetDNSOverrides.ServeStaleDuration", v1alpha2.AKSNodeClass{Spec: v1alpha2.AKSNodeClassSpec{LocalDNS: &v1alpha2.LocalDNS{VnetDNSOverrides: map[string]*v1alpha2.LocalDNSOverrides{"example.com": {ServeStaleDuration: karpv1.MustParseNillableDuration("1h")}}}}}),
	)
	It("should not change hash when tags are changed", func() {
		hash := nodeClass.Hash()
		nodeClass.Spec.Tags = map[string]string{"keyTag-3": "valueTag-3"}
		updatedHash := nodeClass.Hash()
		Expect(hash).To(Equal(updatedHash))
	})
	It("should expect two AKSNodeClasses with the same spec to have the same hash", func() {
		otherNodeClass := &v1alpha2.AKSNodeClass{
			Spec: nodeClass.Spec,
		}
		Expect(nodeClass.Hash()).To(Equal(otherNodeClass.Hash()))
	})
	// This test is a sanity check to update the hashing version if the algorithm has been updated.
	// Note: this will only catch a missing version update, if the staticHash hasn't been updated yet.
	It("when hashing algorithm updates, we should update the hash version", func() {
		currentHashVersion := "v3"
		if nodeClass.Hash() != staticHash {
			Expect(v1alpha2.AKSNodeClassHashVersion).ToNot(Equal(currentHashVersion))
		} else {
			// Note: this failure case is to ensure you have updated currentHashVersion, not AKSNodeClassHashVersion
			Expect(currentHashVersion).To(Equal(v1alpha2.AKSNodeClassHashVersion))
		}
		// Note: this failure case is to ensure you have updated staticHash value
		Expect(staticHash).To(Equal(nodeClass.Hash()))
	})
})
