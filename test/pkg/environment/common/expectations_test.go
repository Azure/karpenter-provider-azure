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

package common_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"
)

func TestExpectations(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Common Environment Expectations")
}

var _ = Describe("Registered NodeClaims for a NodePool", func() {
	var env *common.Environment
	var pool *karpv1.NodePool
	var registered *karpv1.NodeClaim
	var builder *fake.ClientBuilder

	BeforeEach(func() {
		pool = &karpv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}}
		registered = &karpv1.NodeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "registered",
			Labels: map[string]string{
				karpv1.NodePoolLabelKey: pool.Name,
				coretest.DiscoveryLabel: "test",
			},
		}}
		registered.Status.NodeName = "node"
		registered.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
		builder = fake.NewClientBuilder().WithStatusSubresource(&karpv1.NodeClaim{})
		env = &common.Environment{Context: context.Background()}
	})

	DescribeTable("should return only matching live registered claims with node names",
		func(change func(*karpv1.NodeClaim)) {
			excluded := registered.DeepCopy()
			excluded.Name = "excluded"
			change(excluded)
			env.Client = builder.WithObjects(registered, excluded).Build()

			claims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(time.Second, pool, 1)
			Expect(claims).To(HaveLen(1))
			Expect(claims[0].Name).To(Equal(registered.Name))
			Expect(claims[0].Status.NodeName).To(Equal("node"))
		},
		Entry("when another claim is unregistered", func(nc *karpv1.NodeClaim) {
			nc.StatusConditions().SetFalse(karpv1.ConditionTypeRegistered, "Registering", "waiting for registration")
		}),
		Entry("when another claim is terminating", func(nc *karpv1.NodeClaim) {
			nc.DeletionTimestamp = lo.ToPtr(metav1.Now())
			nc.Finalizers = []string{karpv1.TerminationFinalizer}
		}),
		Entry("when another registered claim has no node name", func(nc *karpv1.NodeClaim) {
			nc.Status.NodeName = ""
		}),
		Entry("when another claim belongs to a different pool", func(nc *karpv1.NodeClaim) {
			nc.Labels[karpv1.NodePoolLabelKey] = "other-pool"
		}),
		Entry("when another claim is outside the current test", func(nc *karpv1.NodeClaim) {
			delete(nc.Labels, coretest.DiscoveryLabel)
		}),
	)

	It("should retry list errors and wait for registration", func() {
		pending := registered.DeepCopy()
		pending.Status.NodeName = ""
		pending.StatusConditions().SetFalse(karpv1.ConditionTypeRegistered, "Registering", "waiting for registration")
		attempts := 0
		env.Client = builder.WithObjects(pending).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				attempts++
				if attempts == 1 {
					return errors.New("temporary list failure")
				}
				if attempts == 3 {
					pending.Status = registered.Status
					Expect(c.Status().Update(ctx, pending)).To(Succeed())
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()

		claims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(time.Second, pool, 1)
		Expect(attempts).To(Equal(3))
		Expect(claims).To(HaveLen(1))
		Expect(claims[0].Status.NodeName).To(Equal("node"))
	})

	It("should wait for the exact count, not just a minimum", func() {
		extra := registered.DeepCopy()
		extra.Name = "extra"
		attempts := 0
		env.Client = builder.WithObjects(registered, extra).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				attempts++
				if attempts == 2 {
					Expect(c.Delete(ctx, extra)).To(Succeed())
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()

		claims := env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(time.Second, pool, 1)
		Expect(attempts).To(Equal(2))
		Expect(claims).To(HaveLen(1))
		Expect(claims[0].Name).To(Equal(registered.Name))
	})

	It("should support an empty pool", func() {
		env.Client = builder.Build()
		Expect(env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(time.Second, pool, 0)).To(BeEmpty())
	})

	It("should fail when the requested count does not arrive before the timeout", func() {
		env.Client = builder.Build()
		failures := InterceptGomegaFailures(func() {
			env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(50*time.Millisecond, pool, 1)
		})
		Expect(failures).To(HaveLen(1))
		Expect(failures[0]).To(ContainSubstring("Timed out"))
		Expect(failures[0]).To(ContainSubstring("expected 1 live registered NodeClaims for NodePool pool"))
	})

	It("should honor environment cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		env.Context = ctx
		env.Client = builder.Build()
		failures := InterceptGomegaFailures(func() {
			env.EventuallyExpectRegisteredNodeClaimsForNodePoolWithTimeout(time.Second, pool, 1)
		})
		Expect(failures).To(HaveLen(1))
		Expect(failures[0]).To(ContainSubstring("Context was cancel"))
	})
})
