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

	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeLocalDNSResolver mimics the real resolver by writing Status.LocalDNSState
// onto the in-memory NodeClass when invoked. Tests verify sticky semantics
// surface through Status.LocalDNSState.
type fakeLocalDNSResolver struct {
	state v1beta1.LocalDNSState `json:"-"`
}

func (f *fakeLocalDNSResolver) Resolve(_ context.Context, nc *v1beta1.AKSNodeClass) v1beta1.LocalDNSState {
	// Sticky-Enabled mirror of the real resolver: never flip off if Status is Enabled.
	if nc.Status.LocalDNSState != nil && *nc.Status.LocalDNSState == v1beta1.LocalDNSStateEnabled &&
		nc.Spec.LocalDNS != nil && nc.Spec.LocalDNS.Mode == v1beta1.LocalDNSModePreferred {
		return v1beta1.LocalDNSStateEnabled
	}
	nc.Status.LocalDNSState = lo.ToPtr(f.state)
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

	It("returns true for Required mode via resolver", func() {
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeRequired}
		r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled}
		Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeTrue())
		Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateEnabled))
	})

	It("returns false for Disabled mode via resolver", func() {
		nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModeDisabled}
		r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled}
		Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeFalse())
		Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateDisabled))
	})

	Context("Preferred mode", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{Mode: v1beta1.LocalDNSModePreferred}
		})

		It("returns true via sticky Status (resolver short-circuits)", func() {
			nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled} // would-be flip
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeTrue())
			Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateEnabled))
		})

		It("returns Enabled from resolver and the resolver records Status", func() {
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeTrue())
			Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateEnabled))
		})

		It("returns Disabled from resolver and records Status=Disabled", func() {
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeFalse())
			Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateDisabled))
		})

		It("allows transition from Disabled to Enabled on later evaluation", func() {
			rDisabled := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), rDisabled)).To(BeFalse())
			rEnabled := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), rEnabled)).To(BeTrue())
			Expect(*nodeClass.Status.LocalDNSState).To(Equal(v1beta1.LocalDNSStateEnabled))
		})

		It("returns false when resolver is nil and Status unset", func() {
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeFalse())
		})
	})
})
