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
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeLocalDNSResolver struct {
	state v1beta1.LocalDNSState `json:"-"`
}

func (f *fakeLocalDNSResolver) ResolvePreferred(_ context.Context, _ *v1beta1.AKSNodeClass) v1beta1.LocalDNSState {
	return f.state
}

var _ = Describe("IsLocalDNSEnabled", func() {
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
	})

	It("returns false when LocalDNS spec is nil", func() {
		nodeClass.Spec.LocalDNS = nil
		Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeFalse())
	})

	It("returns true for Required mode", func() {
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}
		Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeTrue())
	})

	It("returns false for Disabled mode", func() {
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
		Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeFalse())
	})

	Context("Preferred mode", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
		})

		It("returns true when Status.LocalDNSState is sticky-Enabled (no resolver call)", func() {
			nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeTrue())
		})

		It("returns Enabled from resolver and persists state", func() {
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeTrue())
			Expect(nodeClass.Status.LocalDNSState).ToNot(BeNil())
			Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateEnabled))
		})

		It("returns Disabled from resolver and overwrites prior Disabled state", func() {
			nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeFalse())
		})

		It("allows transition from Disabled to Enabled (Disabled is not sticky)", func() {
			nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateDisabled)
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeTrue())
			Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateEnabled))
		})
	})
})
