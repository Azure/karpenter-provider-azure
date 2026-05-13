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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeLocalDNSResolver struct {
	state v1beta1.LocalDNSState `json:"-"`
	// persistOnEnabled mimics the real resolver by writing the annotation
	// onto the in-memory NodeClass when it resolves Enabled. The real resolver
	// patches the apiserver; tests just need to verify the sticky semantics
	// surface through the annotation.
	persistOnEnabled bool `json:"-"`
}

func (f *fakeLocalDNSResolver) ResolvePreferred(_ context.Context, nc *v1beta1.AKSNodeClass) v1beta1.LocalDNSState {
	if f.persistOnEnabled && f.state == v1beta1.LocalDNSStateEnabled {
		if nc.Annotations == nil {
			nc.Annotations = map[string]string{}
		}
		nc.Annotations[v1beta1.AnnotationLocalDNSState] = string(v1beta1.LocalDNSStateEnabled)
	}
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

		It("returns true via sticky annotation (no resolver call)", func() {
			nodeClass.Annotations = map[string]string{v1beta1.AnnotationLocalDNSState: string(v1beta1.LocalDNSStateEnabled)}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeTrue())
		})

		It("returns Enabled from resolver and the resolver records the annotation", func() {
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled, persistOnEnabled: true}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeTrue())
			Expect(nodeClass.Annotations[v1beta1.AnnotationLocalDNSState]).To(Equal(string(v1beta1.LocalDNSStateEnabled)))
		})

		It("returns Disabled from resolver without setting annotation (Disabled is not sticky)", func() {
			r := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), r)).To(BeFalse())
			_, hasAnnotation := nodeClass.Annotations[v1beta1.AnnotationLocalDNSState]
			Expect(hasAnnotation).To(BeFalse())
		})

		It("allows transition from no-annotation to Enabled on later evaluation", func() {
			// first resolution: Disabled, no annotation written
			rDisabled := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateDisabled}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), rDisabled)).To(BeFalse())
			// second resolution: Enabled, annotation gets written
			rEnabled := &fakeLocalDNSResolver{state: v1beta1.LocalDNSStateEnabled, persistOnEnabled: true}
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), rEnabled)).To(BeTrue())
			Expect(nodeClass.Annotations[v1beta1.AnnotationLocalDNSState]).To(Equal(string(v1beta1.LocalDNSStateEnabled)))
		})

		It("returns false when resolver is nil and no sticky annotation present", func() {
			Expect(nodeClass.IsLocalDNSEnabled(context.Background(), nil)).To(BeFalse())
		})
	})
})
