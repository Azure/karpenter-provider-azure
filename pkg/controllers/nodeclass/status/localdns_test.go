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

package status_test

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalDNS Reconciler", func() {
	var ctx context.Context
	var reconciler *status.LocalDNSReconciler
	var nodeClass *v1beta1.AKSNodeClass

	BeforeEach(func() {
		ctx = context.Background()
		reconciler = status.NewLocalDNSReconciler()
		nodeClass = &v1beta1.AKSNodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-nodeclass",
				Generation: 1,
			},
			Spec: v1beta1.AKSNodeClassSpec{},
		}
	})

	Context("when LocalDNS is not configured", func() {
		It("should set LocalDNSReady condition to true", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("when LocalDNS has valid zone names", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":             {},
					"example.com":   {},
					"cluster.local": {},
				},
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":                 {},
					"my-domain.com":     {},
					"sub.example.local": {},
				},
			}
		})

		It("should set LocalDNSReady condition to true", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("when VnetDNSOverrides has invalid zone name", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":             {},
					"cluster.local": {},
					"-invalid.com":  {}, // Invalid: starts with hyphen
				},
			}
		})

		It("should set LocalDNSReady condition to false with InvalidConfiguration reason", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonInvalidConfiguration))
			Expect(condition.Message).To(ContainSubstring("Invalid zone name format"))
			Expect(condition.Message).To(ContainSubstring("-invalid.com"))
		})
	})

	Context("when KubeDNSOverrides has invalid zone name", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":           {},
					"example.com": {},
				},
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":            {},
					"invalid..com": {}, // Invalid: double dots
				},
			}
		})

		It("should set LocalDNSReady condition to false with InvalidConfiguration reason", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonInvalidConfiguration))
			Expect(condition.Message).To(ContainSubstring("Invalid zone name format"))
			Expect(condition.Message).To(ContainSubstring("invalid..com"))
		})
	})

	Context("when zone name contains special characters", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":                {},
					"invalid@test.com": {}, // Invalid: contains @
				},
			}
		})

		It("should set LocalDNSReady condition to false", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonInvalidConfiguration))
		})
	})

	Context("when VnetDNSOverrides is nil but KubeDNSOverrides is configured", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode:             v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: nil,
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":           {},
					"example.com": {},
				},
			}
		})

		It("should set LocalDNSReady condition to true (nil maps are allowed)", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("when both override maps are nil", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode:             v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: nil,
				KubeDNSOverrides: nil,
			}
		})

		It("should set LocalDNSReady condition to true (nil maps are allowed)", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsTrue()).To(BeTrue())
		})
	})

	Context("when multiple invalid zone names exist", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: v1beta1.LocalDNSModeRequired,
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".":            {},
					"-invalid.com": {}, // First invalid zone
					"_bad.com":     {}, // Second invalid zone
				},
			}
		})

		It("should fail on the first validation error encountered", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonInvalidConfiguration))
			Expect(condition.Message).To(ContainSubstring("Invalid zone name format"))
		})
	})
})
