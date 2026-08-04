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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/node/health"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/node/interruption"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

var ctx context.Context
var env *coretest.Environment
var azureEnv *test.Environment
var cloudProvider *cloudprovider.CloudProvider
var controller *interruption.Controller
var healthController *health.Controller
var fakeClock *clock.FakeClock

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "SpotInterruption")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(
		coretest.WithCRDs(apis.CRDs...),
		coretest.WithCRDs(v1alpha1.CRDs...),
		coretest.WithFieldIndexers(coretest.NodeClaimProviderIDFieldIndexer(ctx), coretest.NodeProviderIDFieldIndexer(ctx)),
	)
	azureEnv = test.NewEnvironment(ctx, env)
	recorder := events.NewRecorder(&record.FakeRecorder{})
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	fakeClock = clock.NewFakeClock(time.Now())
	controller = interruption.NewController(env.Client, cloudProvider, recorder, fakeClock)
	healthController = health.NewController(env.Client, cloudProvider, fakeClock, recorder)
})

var _ = AfterSuite(func() {
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("Spot Interruption", func() {
	var nodePool *karpv1.NodePool
	var nodeClaim *karpv1.NodeClaim
	var node *corev1.Node

	BeforeEach(func() {
		fakeClock.SetTime(time.Now())
		azureEnv.Reset(ctx)

		nodePool = coretest.NodePool()
		nodeClaim, node = coretest.NodeClaimAndNode(karpv1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{karpv1.TerminationFinalizer},
				Labels:     map[string]string{karpv1.CapacityTypeLabelKey: karpv1.CapacityTypeSpot},
			},
			Spec: karpv1.NodeClaimSpec{
				NodeClassRef: &karpv1.NodeClassReference{
					Group: v1beta1.Group,
					Kind:  "AKSNodeClass",
					Name:  "default",
				},
			},
		})
		node.Labels[karpv1.NodePoolLabelKey] = nodePool.Name
		nodeClaim.Labels[karpv1.NodePoolLabelKey] = nodePool.Name
	})

	AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	Context("Deadline handling", func() {
		It("should drain against the deadline Azure published for a normal ~38s notice", func() {
			deadline := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
			Expect(nodeClaim.DeletionTimestamp).ToNot(BeNil())
		})
		It("should honor a short ~13s notice without extending it", func() {
			deadline := fakeClock.Now().Add(13 * time.Second).UTC().Truncate(time.Second)
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
			Expect(nodeClaim.DeletionTimestamp).ToNot(BeNil())
		})
		It("should still clean up when the deadline has already passed", func() {
			// The notice can be observed late (controller restart, watch lag). There is no drain budget
			// left, but leaving the pods on a node Azure is reclaiming is strictly worse.
			deadline := fakeClock.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
			Expect(nodeClaim.DeletionTimestamp).ToNot(BeNil())
		})
		It("should fall back to immediate cleanup when the deadline cannot be parsed", func() {
			// Failing closed matches the pre-deadline behavior, so it can never be worse than the status
			// quo, and it never silently drops a mandatory preemption.
			node = withPreemptionScheduled(node, "Preempt Scheduled: soon. EventId: not-a-guid")
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, fakeClock.Now().UTC().Format(time.RFC3339)))
			Expect(nodeClaim.DeletionTimestamp).ToNot(BeNil())
		})
		It("should fall back to immediate cleanup when the condition carries no message", func() {
			node = withPreemptionScheduled(node, "")
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, fakeClock.Now().UTC().Format(time.RFC3339)))
			Expect(nodeClaim.DeletionTimestamp).ToNot(BeNil())
		})
	})

	Context("Existing termination timestamp", func() {
		It("should preserve an existing earlier deadline", func() {
			earlier := fakeClock.Now().Add(5 * time.Second).UTC().Truncate(time.Second)
			nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
				karpv1.NodeClaimTerminationTimestampAnnotationKey: earlier.Format(time.RFC3339),
			})
			node = withPreemptionScheduled(node, preemptionMessage(fakeClock.Now().Add(38*time.Second)))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, earlier.Format(time.RFC3339)))
		})
		It("should shorten an existing later deadline", func() {
			// e.g. a NodePool terminationGracePeriod that Azure will not wait for.
			later := fakeClock.Now().Add(time.Hour).UTC().Truncate(time.Second)
			deadline := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
				karpv1.NodeClaimTerminationTimestampAnnotationKey: later.Format(time.RFC3339),
			})
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
		})
		It("should overwrite an unparseable existing deadline", func() {
			deadline := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
				karpv1.NodeClaimTerminationTimestampAnnotationKey: "not-a-timestamp",
			})
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
		})
	})

	Context("Idempotency", func() {
		It("should converge when the same notice is reconciled repeatedly", func() {
			// AKS NPD re-asserts the condition roughly every 2s, so the same notice is observed many
			// times. Nothing may depend on how often it is seen.
			deadline := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)
			first := ExpectExists(ctx, env.Client, nodeClaim)

			for i := 0; i < 3; i++ {
				fakeClock.Step(2 * time.Second)
				ExpectObjectReconciled(ctx, env.Client, controller, node)
			}

			latest := ExpectExists(ctx, env.Client, nodeClaim)
			Expect(latest.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
			Expect(latest.ResourceVersion).To(Equal(first.ResourceVersion))
			Expect(latest.DeletionTimestamp).ToNot(BeNil())
		})
		It("should be a no-op for a NodeClaim that is already terminating", func() {
			// Models a controller restart mid-drain: the condition is replayed as a create event while
			// upstream is already draining the node.
			deadline := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
				karpv1.NodeClaimTerminationTimestampAnnotationKey: deadline.Format(time.RFC3339),
			})
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)
			Expect(env.Client.Delete(ctx, nodeClaim)).To(Succeed())
			terminating := ExpectExists(ctx, env.Client, nodeClaim)
			Expect(terminating.DeletionTimestamp).ToNot(BeNil())

			fakeClock.Step(10 * time.Second)
			ExpectObjectReconciled(ctx, env.Client, controller, node)

			latest := ExpectExists(ctx, env.Client, nodeClaim)
			Expect(latest.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
			Expect(latest.ResourceVersion).To(Equal(terminating.ResourceVersion))
		})
		It("should shorten the deadline of a terminating NodeClaim when Azure moves it earlier", func() {
			original := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			earlier := fakeClock.Now().Add(13 * time.Second).UTC().Truncate(time.Second)
			nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
				karpv1.NodeClaimTerminationTimestampAnnotationKey: original.Format(time.RFC3339),
			})
			node = withPreemptionScheduled(node, preemptionMessage(earlier))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)
			Expect(env.Client.Delete(ctx, nodeClaim)).To(Succeed())

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			latest := ExpectExists(ctx, env.Client, nodeClaim)
			Expect(latest.Annotations).To(HaveKeyWithValue(karpv1.NodeClaimTerminationTimestampAnnotationKey, earlier.Format(time.RFC3339)))
		})
	})

	Context("Scope", func() {
		It("should ignore VMEventScheduled, which also covers reboot, redeploy and freeze", func() {
			node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
				Type:               corev1.NodeConditionType("VMEventScheduled"),
				Status:             corev1.ConditionTrue,
				Reason:             "VMEventScheduled",
				Message:            preemptionMessage(fakeClock.Now().Add(38 * time.Second)),
				LastTransitionTime: metav1.Time{Time: fakeClock.Now()},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).ToNot(HaveKey(karpv1.NodeClaimTerminationTimestampAnnotationKey))
			Expect(nodeClaim.DeletionTimestamp).To(BeNil())
		})
		It("should ignore a PreemptionScheduled condition that is not True", func() {
			node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
				Type:               interruption.ConditionTypePreemptionScheduled,
				Status:             corev1.ConditionFalse,
				Message:            preemptionMessage(fakeClock.Now().Add(38 * time.Second)),
				LastTransitionTime: metav1.Time{Time: fakeClock.Now()},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).ToNot(HaveKey(karpv1.NodeClaimTerminationTimestampAnnotationKey))
			Expect(nodeClaim.DeletionTimestamp).To(BeNil())
		})
		It("should ignore nodes that are not managed by this cloud provider", func() {
			delete(node.Labels, karpv1.NodeClassLabelKey(schemaGroupKind()))
			node = withPreemptionScheduled(node, preemptionMessage(fakeClock.Now().Add(38*time.Second)))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			ExpectObjectReconciled(ctx, env.Client, controller, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).ToNot(HaveKey(karpv1.NodeClaimTerminationTimestampAnnotationKey))
			Expect(nodeClaim.DeletionTimestamp).To(BeNil())
		})
		It("should not error when the node has no NodeClaim", func() {
			node = withPreemptionScheduled(node, preemptionMessage(fakeClock.Now().Add(38*time.Second)))
			ExpectApplied(ctx, env.Client, nodePool, node)

			result, err := controller.Reconcile(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})
		It("should not error when the NodeClaim was already removed", func() {
			node = withPreemptionScheduled(node, preemptionMessage(fakeClock.Now().Add(38*time.Second)))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)
			ExpectObjectReconciled(ctx, env.Client, controller, node)
			ExpectFinalizersRemoved(ctx, env.Client, nodeClaim)
			ExpectNotFound(ctx, env.Client, nodeClaim)

			result, err := controller.Reconcile(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})
	})

	Context("Node repair interaction", func() {
		// These two specs pin the decision to take Spot preemption out of the generic repair path.
		// node.health treats every RepairPolicy identically: it overwrites the termination timestamp with
		// `now` (collapsing every pod's grace period) and gates on the cluster-wide unhealthy-node
		// circuit breaker (which can hold the repair past the eviction deadline).
		It("should not advertise PreemptionScheduled as a repair policy", func() {
			Expect(cloudProvider.RepairPolicies()).ToNot(ContainElement(HaveField("ConditionType", interruption.ConditionTypePreemptionScheduled)))
		})
		It("should leave PreemptionScheduled nodes untouched by node repair", func() {
			deadline := fakeClock.Now().Add(38 * time.Second).UTC().Truncate(time.Second)
			node = withPreemptionScheduled(node, preemptionMessage(deadline))
			ExpectApplied(ctx, env.Client, nodePool, nodeClaim, node)

			// Push well past every repair policy's toleration window so that only policy membership,
			// not timing, decides the outcome.
			fakeClock.Step(time.Hour)
			ExpectObjectReconciled(ctx, env.Client, healthController, node)

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).ToNot(HaveKey(karpv1.NodeClaimTerminationTimestampAnnotationKey))
			Expect(nodeClaim.DeletionTimestamp).To(BeNil())
		})
		It("should keep the other repair policies", func() {
			Expect(cloudProvider.RepairPolicies()).To(ContainElements(
				HaveField("ConditionType", corev1.NodeReady),
				HaveField("ConditionType", corev1.NodeConditionType("kubernetes.azure.com/NodeHealthy")),
			))
		})
	})
})

// preemptionMessage renders the condition message shape emitted by the AKS node-problem-detector.
func preemptionMessage(notBefore time.Time) string {
	return fmt.Sprintf("Preempt Scheduled: %s. For more information, see https://aka.ms/aks-spot-eviction. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		notBefore.UTC().Format(time.RFC1123))
}

func withPreemptionScheduled(node *corev1.Node, message string) *corev1.Node {
	node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
		Type:               interruption.ConditionTypePreemptionScheduled,
		Status:             corev1.ConditionTrue,
		Reason:             interruption.ConditionReasonSpotEvictionIncoming,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: fakeClock.Now()},
	})
	return node
}

func schemaGroupKind() schema.GroupKind {
	return object.GVK(&v1beta1.AKSNodeClass{}).GroupKind()
}

var _ corecloudprovider.CloudProvider = &cloudprovider.CloudProvider{}
