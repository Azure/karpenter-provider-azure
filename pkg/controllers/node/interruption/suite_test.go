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

package interruption_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/node/health"
	"sigs.k8s.io/karpenter/pkg/controllers/node/termination"
	"sigs.k8s.io/karpenter/pkg/controllers/node/termination/terminator"
	"sigs.k8s.io/karpenter/pkg/controllers/nodeclaim/lifecycle"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/node/interruption"
)

var ctx context.Context
var env *coretest.Environment
var provider = &cloudprovider.CloudProvider{}
var recorder *coretest.EventRecorder
var controller *interruption.Controller
var tracked *trackingClient

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Spot Interruption")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	env = coretest.NewEnvironment(
		coretest.WithCRDs(apis.CRDs...),
		coretest.WithFieldIndexers(coretest.NodeClaimProviderIDFieldIndexer(ctx), coretest.NodeProviderIDFieldIndexer(ctx)),
	)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("Spot Interruption", func() {
	var node *corev1.Node
	var claim *karpv1.NodeClaim
	var pool *karpv1.NodePool
	var deadline time.Time

	BeforeEach(func() {
		env.Clock.SetTime(time.Now().UTC().Truncate(time.Second))
		deadline = env.Clock.Now().Add(30 * time.Second)
		recorder = coretest.NewEventRecorder()
		tracked = &trackingClient{Client: env.Client}
		controller = interruption.NewController(tracked, provider, recorder, env.Clock)
		pool = coretest.NodePool()
		claim, node = coretest.NodeClaimAndNode(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{karpv1.TerminationFinalizer},
				Labels:     map[string]string{karpv1.CapacityTypeLabelKey: karpv1.CapacityTypeSpot},
			},
			Spec: karpv1.NodeClaimSpec{NodeClassRef: &karpv1.NodeClassReference{
				Group: v1beta1.Group, Kind: "AKSNodeClass", Name: "default",
			}},
		})
		claim.Labels[karpv1.NodePoolLabelKey] = pool.Name
		node.Labels[karpv1.NodePoolLabelKey] = pool.Name
		setNotice(node, preemptMessage(deadline))
	})

	AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	DescribeTable("leaves advisory and unknown signals untouched", func(message, eventReason string) {
		setNotice(node, message)
		pod := coretest.Pod(coretest.PodOptions{NodeName: node.Name, TerminationGracePeriodSeconds: lo.ToPtr[int64](360)})
		ExpectApplied(ctx, env.Client, pool, claim, node, pod)
		originalNode := ExpectExists(ctx, env.Client, node)
		originalPod := ExpectExists(ctx, env.Client, pod)
		originalClaim := ExpectExists(ctx, env.Client, claim)

		for i := 0; i < 3; i++ {
			ExpectObjectReconciled(ctx, env.Client, controller, node)
		}

		Expect(tracked.patches).To(BeZero())
		Expect(tracked.deletes).To(BeZero())
		Expect(ExpectExists(ctx, env.Client, node)).To(Equal(originalNode))
		Expect(ExpectExists(ctx, env.Client, pod)).To(Equal(originalPod))
		Expect(ExpectExists(ctx, env.Client, claim)).To(Equal(originalClaim))
		claims := &karpv1.NodeClaimList{}
		Expect(env.Client.List(ctx, claims)).To(Succeed())
		Expect(claims.Items).To(HaveLen(1))
		Expect(recorder.Calls(eventReason)).To(BeNumerically(">", 0))
	},
		Entry("advisory without a date", "SpotRebalanceRecommendation Advisory: . For more information, see https://example.com.", "SpotRebalanceAdvisory"),
		Entry("advisory with a date", "SpotRebalanceRecommendation Advisory: Sat, 01 Aug 2026 12:00:30 GMT.", "SpotRebalanceAdvisory"),
		Entry("empty message", "", "UnknownSpotInterruption"),
		Entry("ambiguous message", "Spot eviction may happen soon", "UnknownSpotInterruption"),
		Entry("other event", "Reboot Scheduled: Sat, 01 Aug 2026 12:00:30 GMT.", "UnknownSpotInterruption"),
	)

	DescribeTable("preserves the published notice", func(notice time.Duration) {
		deadline = env.Clock.Now().Add(notice)
		setNotice(node, preemptMessage(deadline))
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		claim = ExpectExists(ctx, env.Client, claim)
		Expect(claim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
		Expect(claim.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(tracked.operations).To(Equal([]string{"patch", "delete"}))
	}, Entry("normal", 30*time.Second), Entry("short", 5*time.Second), Entry("expired", -time.Minute))

	DescribeTable("cleans up positively identified Preempt without usable remaining notice", func(message, warning string) {
		setNotice(node, message)
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		claim = ExpectExists(ctx, env.Client, claim)
		Expect(claim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, env.Clock.Now().Format(time.RFC3339)))
		Expect(claim.DeletionTimestamp.IsZero()).To(BeFalse())
		if warning != "" {
			Expect(recorder.Calls(warning)).To(BeNumerically(">", 0))
		}
	},
		Entry("Started without a date", "Preempt Started: .", ""),
		Entry("Started ignores future date", "Preempt Started: Tue, 01 Jan 2097 00:00:00 GMT.", ""),
		Entry("Scheduled malformed deadline", "Preempt Scheduled: soon.", "UnknownSpotEvictionDeadline"),
		Entry("Scheduled missing deadline", "Preempt Scheduled:", "UnknownSpotEvictionDeadline"),
	)

	It("handles advisory -> Preempt without a status or transition-time change", func() {
		setNotice(node, "SpotRebalanceRecommendation Advisory:")
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(tracked.operations).To(BeEmpty())
		node = ExpectExists(ctx, env.Client, node)
		node.Status.Conditions[0].Message = preemptMessage(deadline)
		Expect(env.Client.Status().Update(ctx, node)).To(Succeed())
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeFalse())
	})

	DescribeTable("honors existing deadlines", func(existing string, terminating bool, expectedOffset time.Duration) {
		switch existing {
		case "earlier":
			existing = env.Clock.Now().Add(5 * time.Second).Format(time.RFC3339)
		case "later":
			existing = env.Clock.Now().Add(time.Hour).Format(time.RFC3339)
		}
		claim.Annotations = map[string]string{karpv1.NodeClaimTerminationTimestampAnnotationKey: existing}
		ExpectApplied(ctx, env.Client, pool, claim, node)
		if terminating {
			Expect(env.Client.Delete(ctx, claim)).To(Succeed())
		}
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		claim = ExpectExists(ctx, env.Client, claim)
		Expect(claim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, env.Clock.Now().Add(expectedOffset).Format(time.RFC3339)))
		Expect(claim.DeletionTimestamp.IsZero()).To(BeFalse())
		if terminating {
			Expect(tracked.deletes).To(BeZero())
		}
	},
		Entry("earlier", "earlier", false, 5*time.Second),
		Entry("later", "later", false, 30*time.Second),
		Entry("invalid annotation", "invalid", false, 30*time.Second),
		Entry("already terminating earlier", "earlier", true, 5*time.Second),
		Entry("already terminating later", "later", true, 30*time.Second),
	)

	It("preserves an earlier in-progress grace period before lifecycle has annotated it", func() {
		claim.Spec.TerminationGracePeriod = &metav1.Duration{Duration: time.Second}
		ExpectApplied(ctx, env.Client, pool, claim, node)
		Expect(env.Client.Delete(ctx, claim)).To(Succeed())
		claim = ExpectExists(ctx, env.Client, claim)
		expected := claim.DeletionTimestamp.Add(time.Second)
		Expect(expected.Before(deadline)).To(BeTrue())
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(ExpectExists(ctx, env.Client, claim).Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, expected.Format(time.RFC3339)))
		Expect(tracked.deletes).To(BeZero())
	})

	It("converges across repeated reconciles and a controller restart", func() {
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		first := ExpectExists(ctx, env.Client, claim)
		controller = interruption.NewController(tracked, provider, recorder, env.Clock)
		for i := 0; i < 3; i++ {
			env.Clock.Step(time.Second)
			ExpectObjectReconciled(ctx, env.Client, controller, node)
		}
		Expect(ExpectExists(ctx, env.Client, claim).ResourceVersion).To(Equal(first.ResourceVersion))
		Expect(tracked.operations).To(Equal([]string{"patch", "delete"}))
	})

	It("re-reads an earlier concurrent deadline on an optimistic patch conflict", func() {
		ExpectApplied(ctx, env.Client, pool, claim, node)
		earlier := env.Clock.Now().Add(5 * time.Second).Format(time.RFC3339)
		tracked.beforePatch = func() {
			latest := ExpectExists(ctx, env.Client, claim)
			latest.Annotations = map[string]string{karpv1.NodeClaimTerminationTimestampAnnotationKey: earlier}
			Expect(env.Client.Update(ctx, latest)).To(Succeed())
		}
		_, err := controller.Reconcile(ctx, node)
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		Expect(tracked.deletes).To(BeZero())
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeTrue())

		tracked.beforePatch = nil
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		claim = ExpectExists(ctx, env.Client, claim)
		Expect(claim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, earlier))
		Expect(claim.DeletionTimestamp.IsZero()).To(BeFalse())
	})

	It("protects deletion against a concurrent deadline change and retries safely", func() {
		ExpectApplied(ctx, env.Client, pool, claim, node)
		earlier := env.Clock.Now().Add(5 * time.Second).Format(time.RFC3339)
		tracked.beforeDelete = func() {
			latest := ExpectExists(ctx, env.Client, claim)
			latest.Annotations[karpv1.NodeClaimTerminationTimestampAnnotationKey] = earlier
			Expect(env.Client.Update(ctx, latest)).To(Succeed())
		}
		_, err := controller.Reconcile(ctx, node)
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeTrue())
		tracked.beforeDelete = nil
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(ExpectExists(ctx, env.Client, claim).Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, earlier))
	})

	DescribeTable("surfaces API failures without losing the deadline", func(operation string) {
		ExpectApplied(ctx, env.Client, pool, claim, node)
		tracked.fail = operation
		_, err := controller.Reconcile(ctx, node)
		Expect(err).To(MatchError(ContainSubstring("synthetic API failure")))
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeTrue())
		if operation == "delete" {
			Expect(ExpectExists(ctx, env.Client, claim).Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
		} else {
			Expect(tracked.deletes).To(BeZero())
		}
		tracked.fail = ""
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeFalse())
	}, Entry("list", "list"), Entry("patch", "patch"), Entry("delete", "delete"))

	DescribeTable("ignores unrelated and unmanaged signals", func(modify func(*karpv1.NodeClaim, *corev1.Node)) {
		modify(claim, node)
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(tracked.operations).To(BeEmpty())
	},
		Entry("VMEventScheduled", func(_ *karpv1.NodeClaim, n *corev1.Node) { n.Status.Conditions[0].Type = "VMEventScheduled" }),
		Entry("false condition", func(_ *karpv1.NodeClaim, n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionFalse }),
		Entry("unknown condition", func(_ *karpv1.NodeClaim, n *corev1.Node) { n.Status.Conditions[0].Status = corev1.ConditionUnknown }),
		Entry("unmanaged node", func(_ *karpv1.NodeClaim, n *corev1.Node) {
			delete(n.Labels, karpv1.NodeClassLabelKey(object.GVK(&v1beta1.AKSNodeClass{}).GroupKind()))
		}),
		Entry("unmanaged claim", func(nc *karpv1.NodeClaim, _ *corev1.Node) { nc.Spec.NodeClassRef.Group = "other.example.com" }),
		Entry("empty provider ID", func(_ *karpv1.NodeClaim, n *corev1.Node) { n.Spec.ProviderID = "" }),
	)

	It("does not rely on the reason to distinguish Preempt from an advisory", func() {
		node.Status.Conditions[0].Reason = "RenamedReason"
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeFalse())
	})

	It("leaves missing claims untouched and handles a subsequently available claim", func() {
		ExpectApplied(ctx, env.Client, pool, node)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(tracked.operations).To(BeEmpty())
		ExpectApplied(ctx, env.Client, claim)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(ExpectExists(ctx, env.Client, claim).DeletionTimestamp.IsZero()).To(BeFalse())
		ExpectFinalizersRemoved(ctx, env.Client, claim)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		Expect(tracked.deletes).To(Equal(1))
	})

	It("refuses ambiguous claims", func() {
		duplicate := claim.DeepCopy()
		duplicate.Name += "-duplicate"
		ExpectApplied(ctx, env.Client, pool, claim, duplicate, node)
		_, err := controller.Reconcile(ctx, node)
		Expect(err).To(MatchError(ContainSubstring("multiple nodeclaims")))
		Expect(tracked.operations).To(BeEmpty())
	})

	DescribeTable("passes available notice through upstream lifecycle and pod termination", func(grace int64, notice, elapsed time.Duration, expected int64) {
		deadline = env.Clock.Now().Add(notice)
		setNotice(node, preemptMessage(deadline))
		claim.Spec.TerminationGracePeriod = &metav1.Duration{Duration: time.Hour}
		claim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
		pod := coretest.Pod(coretest.PodOptions{
			NodeName:                      node.Name,
			ObjectMeta:                    metav1.ObjectMeta{Annotations: map[string]string{karpv1.DoNotDisruptAnnotationKey: "true"}},
			TerminationGracePeriodSeconds: lo.ToPtr(grace),
		})
		ExpectApplied(ctx, env.Client, pool, claim, node, pod)
		ExpectObjectReconciled(ctx, env.Client, controller, node)
		ExpectObjectReconciled(ctx, env.Client, health.NewController(env.Client, provider, env.Clock, recorder), node)

		lifecycleController := lifecycle.NewController(env.Clock, env.Client, provider, recorder, nil, nil)
		ExpectObjectReconciled(ctx, env.Client, lifecycleController, claim)
		Expect(ExpectExists(ctx, env.Client, claim).Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
		Expect(ExpectExists(ctx, env.Client, node).DeletionTimestamp.IsZero()).To(BeFalse())
		env.Clock.Step(elapsed)
		queue := terminator.NewQueue(env.Client, recorder)
		terminationController := termination.NewController(env.Clock, env.Client, provider, terminator.NewTerminator(env.Clock, env.Client, queue, recorder), recorder)
		ExpectObjectReconciled(ctx, env.Client, terminationController, node)

		pod = ExpectExists(ctx, env.Client, pod)
		Expect(pod.DeletionGracePeriodSeconds).ToNot(BeNil())
		Expect(*pod.DeletionGracePeriodSeconds).To(Equal(expected))
	},
		Entry("180s pod gets remaining 25s, not 1s", int64(180), 30*time.Second, 5*time.Second, int64(25)),
		Entry("360s pod gets remaining 25s, not 1s", int64(360), 30*time.Second, 5*time.Second, int64(25)),
		Entry("short notice remains short", int64(180), 5*time.Second, 2*time.Second, int64(3)),
		Entry("expired notice keeps the upstream 1s floor", int64(360), -time.Minute, time.Duration(0), int64(1)),
	)

	It("retains exactly the non-Spot repair policies", func() {
		Expect(provider.RepairPolicies()).To(ConsistOf(
			corecloudprovider.RepairPolicy{ConditionType: corev1.NodeReady, ConditionStatus: corev1.ConditionFalse, TolerationDuration: 10 * time.Minute},
			corecloudprovider.RepairPolicy{ConditionType: corev1.NodeReady, ConditionStatus: corev1.ConditionUnknown, TolerationDuration: 10 * time.Minute},
			corecloudprovider.RepairPolicy{ConditionType: "kubernetes.azure.com/NodeHealthy", ConditionStatus: corev1.ConditionFalse},
		))
		Expect(cloudprovider.SpotConditionPreemptionScheduled).To(Equal(string(interruption.ConditionTypePreemptionScheduled)))
	})

	DescribeTable("keeps Spot signals out of generic node health", func(message string) {
		setNotice(node, message)
		ExpectApplied(ctx, env.Client, pool, claim, node)
		env.Clock.Step(time.Hour)
		healthController := health.NewController(tracked, provider, env.Clock, recorder)
		ExpectObjectReconciled(ctx, env.Client, healthController, node)
		Expect(tracked.operations).To(BeEmpty())
	}, Entry("advisory", "SpotRebalanceRecommendation Advisory:"), Entry("Preempt", "Preempt Scheduled: Sat, 01 Aug 2026 12:00:30 GMT."))

	DescribeTable("retains ordinary health repair even with an advisory", func(conditionType corev1.NodeConditionType, conditionStatus corev1.ConditionStatus) {
		setNotice(node, "SpotRebalanceRecommendation Advisory:")
		node.Status.Conditions[1] = corev1.NodeCondition{
			Type: conditionType, Status: conditionStatus, LastTransitionTime: metav1.NewTime(env.Clock.Now().Add(-11 * time.Minute)),
		}
		ExpectApplied(ctx, env.Client, pool, claim, node)
		ExpectObjectReconciled(ctx, env.Client, health.NewController(env.Client, provider, env.Clock, recorder), node)
		claim = ExpectExists(ctx, env.Client, claim)
		Expect(claim.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(claim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, env.Clock.Now().Format(time.RFC3339)))
	},
		Entry("Ready False", corev1.NodeReady, corev1.ConditionFalse),
		Entry("Ready Unknown", corev1.NodeReady, corev1.ConditionUnknown),
		Entry("NodeHealthy False", corev1.NodeConditionType("kubernetes.azure.com/NodeHealthy"), corev1.ConditionFalse),
	)
})

func setNotice(node *corev1.Node, message string) {
	node.Status.Conditions = []corev1.NodeCondition{{
		Type: interruption.ConditionTypePreemptionScheduled, Status: corev1.ConditionTrue,
		Reason: "SpotEvictionIncoming", Message: message, LastTransitionTime: metav1.NewTime(env.Clock.Now()),
	}, {Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(env.Clock.Now())}}
}

func preemptMessage(deadline time.Time) string {
	return fmt.Sprintf("Preempt Scheduled: %s. For more information, see https://example.com. EventId: 00000000-0000-0000-0000-000000000001", deadline.UTC().Format(time.RFC1123))
}

type trackingClient struct {
	client.Client
	patches, deletes          int
	operations                []string
	beforePatch, beforeDelete func()
	fail                      string
}

func (c *trackingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.fail == "list" {
		return fmt.Errorf("synthetic API failure")
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *trackingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches++
	c.operations = append(c.operations, "patch")
	if c.fail == "patch" {
		return fmt.Errorf("synthetic API failure")
	}
	if c.beforePatch != nil {
		c.beforePatch()
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *trackingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deletes++
	c.operations = append(c.operations, "delete")
	if c.fail == "delete" {
		return fmt.Errorf("synthetic API failure")
	}
	if c.beforeDelete != nil {
		c.beforeDelete()
	}
	return c.Client.Delete(ctx, obj, opts...)
}
