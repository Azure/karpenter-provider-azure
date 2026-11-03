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

package localdns_test

import (
	"fmt"

	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/localdns"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// skuRequirements builds the two instance type requirements the LocalDNS VM size
// floor is read from. The instance type provider stamps these with raw skewer
// values, so the numbers here are the SKU's advertised vCPU count and memory in
// MiB -- not the overhead-adjusted values in InstanceType.Capacity.
func skuRequirements(vcpu, memoryMiB int64) scheduling.Requirements {
	return scheduling.NewRequirements(
		scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, fmt.Sprint(vcpu)),
		scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, fmt.Sprint(memoryMiB)),
	)
}

var _ = Describe("InstanceTypeMeetsFloor", func() {
	DescribeTable("applies the VM size floor to instance type requirements",
		func(reqs scheduling.Requirements, expected bool) {
			Expect(localdns.InstanceTypeMeetsFloor(reqs)).To(Equal(expected))
		},
		Entry("Standard_D4s_v3 (4 vCPU / 16 GiB)", skuRequirements(4, 16384), true),
		Entry("Standard_D2s_v3 (2 vCPU / 8 GiB)", skuRequirements(2, 8192), false),
		Entry("exactly at the floor", skuRequirements(localdns.MinVCPU, localdns.MinMemoryMiB), true),
		Entry("one vCPU short", skuRequirements(localdns.MinVCPU-1, 16384), false),
		Entry("one MiB short", skuRequirements(8, localdns.MinMemoryMiB-1), false),
		// A SKU we can't size is a SKU we can't vouch for, so it doesn't get LocalDNS.
		Entry("no requirements", scheduling.NewRequirements(), false),
		Entry("vCPU only", scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, "8")), false),
		Entry("memory only", scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, "32768")), false),
		Entry("non-numeric vCPU", scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, "many"),
			scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, "32768")), false),
		// An unresolved requirement (still a set of candidate values) isn't a
		// single VM size, so there's nothing to check the floor against.
		Entry("ambiguous vCPU", scheduling.NewRequirements(
			scheduling.NewRequirement(v1beta1.LabelSKUCPU, corev1.NodeSelectorOpIn, "4", "8"),
			scheduling.NewRequirement(v1beta1.LabelSKUMemory, corev1.NodeSelectorOpIn, "32768")), false),
	)
})

var _ = Describe("IsSupportedForInstanceType", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	DescribeTable("requires both the NodeClass verdict and a big enough VM size",
		func(state *v1beta1.LocalDNSState, reqs scheduling.Requirements, expected bool) {
			nodeClass.Status.LocalDNSState = state
			Expect(localdns.IsSupportedForInstanceType(nodeClass, reqs)).To(Equal(expected))
		},
		Entry("enabled + capable SKU", lo.ToPtr(v1beta1.LocalDNSStateEnabled), skuRequirements(4, 16384), true),
		Entry("enabled + too-small SKU", lo.ToPtr(v1beta1.LocalDNSStateEnabled), skuRequirements(2, 8192), false),
		Entry("disabled + capable SKU", lo.ToPtr(v1beta1.LocalDNSStateDisabled), skuRequirements(4, 16384), false),
		Entry("nil state + capable SKU", (*v1beta1.LocalDNSState)(nil), skuRequirements(4, 16384), false),
	)
})

var _ = Describe("ResolveForWire", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("returns nil when LocalDNS is unset", func() {
		nodeClass.Spec.LocalDNS = nil
		Expect(localdns.ResolveForWire(nodeClass, skuRequirements(4, 16384))).To(BeNil())
	})

	DescribeTable("passes non-Preferred modes through untouched",
		func(mode v1beta1.LocalDNSMode) {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: mode}
			nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
			// Even a SKU below the floor doesn't rewrite Required: that filtering
			// happens up front in the instance type provider, so reaching here with
			// a too-small SKU would be a bug we shouldn't paper over on the wire.
			Expect(localdns.ResolveForWire(nodeClass, skuRequirements(2, 8192)).Mode).To(Equal(mode))
		},
		Entry("Required", v1beta1.LocalDNSModeRequired),
		Entry("Disabled", v1beta1.LocalDNSModeDisabled),
	)

	// Preferred is resolved per node: AKS turns LocalDNS on where the VM size can
	// carry it and off where it can't, rather than refusing to provision. The wire
	// format has no Preferred, so it lands as Required or Disabled.
	DescribeTable("resolves Preferred against the node's own VM size",
		func(state *v1beta1.LocalDNSState, reqs scheduling.Requirements, expected v1beta1.LocalDNSMode) {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
			nodeClass.Status.LocalDNSState = state
			Expect(localdns.ResolveForWire(nodeClass, reqs).Mode).To(Equal(expected))
		},
		Entry("enabled + capable SKU", lo.ToPtr(v1beta1.LocalDNSStateEnabled), skuRequirements(4, 16384), v1beta1.LocalDNSModeRequired),
		Entry("enabled + too-small SKU", lo.ToPtr(v1beta1.LocalDNSStateEnabled), skuRequirements(2, 8192), v1beta1.LocalDNSModeDisabled),
		Entry("disabled + capable SKU", lo.ToPtr(v1beta1.LocalDNSStateDisabled), skuRequirements(4, 16384), v1beta1.LocalDNSModeDisabled),
	)

	It("does not mutate the spec when resolving Preferred", func() {
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
		nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)

		Expect(localdns.ResolveForWire(nodeClass, skuRequirements(4, 16384)).Mode).To(Equal(v1beta1.LocalDNSModeRequired))
		Expect(nodeClass.Spec.LocalDNS.Mode).To(Equal(v1beta1.LocalDNSModePreferred))
		// Two nodes off one NodeClass can land on different answers, so the first
		// call must not poison the second.
		Expect(localdns.ResolveForWire(nodeClass, skuRequirements(2, 8192)).Mode).To(Equal(v1beta1.LocalDNSModeDisabled))
	})

	It("carries the zone overrides through", func() {
		overrides := []v1beta1.LocalDNSZoneOverride{{Zone: "example.com", Protocol: v1beta1.LocalDNSProtocolForceTCP}}
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred, VnetDNSOverrides: overrides}
		nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)

		Expect(localdns.ResolveForWire(nodeClass, skuRequirements(4, 16384)).VnetDNSOverrides).To(Equal(overrides))
	})
})
