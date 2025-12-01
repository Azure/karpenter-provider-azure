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
	"github.com/samber/lo"
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

	Context("when LocalDNS is properly configured", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
					},
					"cluster.local": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
				},
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
					},
					"cluster.local": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
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

	Context("when VnetDNSOverrides is missing required zones", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
					},
					// Missing "cluster.local"
				},
			}
		})

		It("should set LocalDNSReady condition to false with MissingRequiredZones reason", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonMissingRequiredZones))
			Expect(condition.Message).To(ContainSubstring("vnetDNSOverrides must contain required zones"))
		})
	})

	Context("when KubeDNSOverrides is missing required zones", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					"cluster.local": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
					// Missing "."
				},
			}
		})

		It("should set LocalDNSReady condition to false with MissingRequiredZones reason", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonMissingRequiredZones))
			Expect(condition.Message).To(ContainSubstring("kubeDNSOverrides must contain required zones"))
		})
	})

	Context("when VnetDNSOverrides has invalid forwarding rules", func() {
		Context("cluster.local forwarded to VnetDNS", func() {
			BeforeEach(func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
					VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
						".": {
							ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
						},
						"cluster.local": {
							ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS), // Invalid!
						},
					},
				}
			})

			It("should set LocalDNSReady condition to false with InvalidForwarding reason", func() {
				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonInvalidForwarding))
				Expect(condition.Message).To(ContainSubstring("cluster.local' cannot be forwarded to VnetDNS"))
			})
		})

		Context("root zone '.' forwarded to ClusterCoreDNS", func() {
			BeforeEach(func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
					VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
						".": {
							ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS), // Invalid!
						},
						"cluster.local": {
							ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
						},
					},
				}
			})

			It("should set LocalDNSReady condition to false with InvalidForwarding reason", func() {
				result, err := reconciler.Reconcile(ctx, nodeClass)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
				Expect(condition.IsFalse()).To(BeTrue())
				Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonInvalidForwarding))
				Expect(condition.Message).To(ContainSubstring("root zone '.' cannot be forwarded to ClusterCoreDNS"))
			})
		})
	})

	Context("when serveStale 'Verify' is used with protocol 'ForceTCP'", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
						Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolForceTCP),
						ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleVerify), // Invalid combination!
					},
					"cluster.local": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
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
			Expect(condition.Message).To(ContainSubstring("serveStale 'Verify' cannot be used with protocol 'ForceTCP'"))
		})
	})

	Context("when serveStale 'Verify' is used with protocol 'PreferUDP'", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
						Protocol:           lo.ToPtr(v1beta1.LocalDNSProtocolPreferUDP),
						ServeStale:         lo.ToPtr(v1beta1.LocalDNSServeStaleVerify), // Valid combination
					},
					"cluster.local": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
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

	Context("when VnetDNSOverrides is nil but KubeDNSOverrides is configured", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: nil,
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
					},
					"cluster.local": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
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
				Mode:             lo.ToPtr(v1beta1.LocalDNSModeRequired),
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

	Context("when multiple validation errors exist", func() {
		BeforeEach(func() {
			nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
				Mode: lo.ToPtr(v1beta1.LocalDNSModeRequired),
				VnetDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
					},
					// Missing "cluster.local"
				},
				KubeDNSOverrides: map[string]*v1beta1.LocalDNSOverrides{
					".": {
						ForwardDestination: lo.ToPtr(v1beta1.LocalDNSForwardDestinationVnetDNS),
					},
					// Also missing "cluster.local"
				},
			}
		})

		It("should fail on the first validation error encountered (VnetDNSOverrides)", func() {
			result, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			condition := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeLocalDNSReady)
			Expect(condition.IsFalse()).To(BeTrue())
			Expect(condition.Reason).To(Equal(status.LocalDNSUnreadyReasonMissingRequiredZones))
			Expect(condition.Message).To(ContainSubstring("vnetDNSOverrides"))
		})
	})
})
