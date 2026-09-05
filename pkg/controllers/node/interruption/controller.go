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

// Package interruption reacts to Azure Spot preemption notices.
//
// # Ownership
//
// This controller owns the response to the `PreemptionScheduled` Node condition for Nodes managed by
// this cloud provider. It is deliberately *not* modeled as a `cloudprovider.RepairPolicy`: upstream's
// `node.health` controller treats every repair policy identically, stamping
// `karpenter.sh/nodeclaim-termination-timestamp` with `now` (which collapses every pod's grace period
// to ~1s) and gating the repair behind the cluster-wide unhealthy-node circuit breaker (which can delay
// action past the point where the VM is already gone). Neither behavior is correct for a Spot notice
// that carries a real, known deadline. Registering `PreemptionScheduled` in both places would let the
// two controllers race, and `node.health` would win by overwriting the real deadline with `now`.
//
// # Deadline semantics
//
// Azure publishes a Scheduled Event with a `NotBefore` timestamp: a guarantee that the VM will not be
// evicted before that instant. Observed notice for a regular Spot preemption is roughly 30s, but it can
// be materially shorter, and it is never longer than Azure decides. The deadline is therefore treated
// as a hard budget, never as a promise:
//
//   - `NotBefore` is written to `karpenter.sh/nodeclaim-termination-timestamp` before the NodeClaim is
//     deleted, so upstream's NodeClaim lifecycle preserves it rather than deriving its own.
//   - Upstream `node.termination` then taints the node `NoSchedule` immediately, drains through the
//     normal eviction queue (honoring PDBs and `karpenter.sh/do-not-disrupt` while time remains),
//     clamps each pod's grace period to the time actually left, preemptively deletes pods whose full
//     grace period no longer fits, and stops waiting on volume detachment at the deadline.
//   - Everything after the deadline passes is best effort. Karpenter cannot stop Azure from reclaiming
//     the VM, and this controller makes no attempt to pre-provision replacement capacity: replacement is
//     reactive, driven by the pods that become unschedulable during the drain.
package interruption

import (
	"context"
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/events"
	"sigs.k8s.io/karpenter/pkg/metrics"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	nodeutils "sigs.k8s.io/karpenter/pkg/utils/node"
)

// SpotInterruptionReason labels NodeClaims disrupted by an Azure Spot preemption notice.
const SpotInterruptionReason = "spot_interruption"

// Controller drains Azure Spot nodes against the eviction deadline Azure published for them.
type Controller struct {
	kubeClient    client.Client
	cloudProvider cloudprovider.CloudProvider
	recorder      events.Recorder
	clock         clock.Clock
}

// NewController constructs a controller instance.
func NewController(kubeClient client.Client, cloudProvider cloudprovider.CloudProvider, recorder events.Recorder, clk clock.Clock) *Controller {
	return &Controller{
		kubeClient:    kubeClient,
		cloudProvider: cloudProvider,
		recorder:      recorder,
		clock:         clk,
	}
}

func (c *Controller) Name() string {
	return "node.interruption"
}

// Reconcile is level-triggered on the Node's `PreemptionScheduled` condition rather than edge-triggered
// on `PreemptScheduled` Kubernetes Events. Azure republishes the same notice roughly every 2s for as long
// as it is outstanding, so event counts are meaningless; the condition is the durable signal and survives
// a controller restart. Every step below is a no-op when it has already been performed, so duplicate
// reconciles, replayed watch events, and restarts all converge on the same state.
func (c *Controller) Reconcile(ctx context.Context, node *corev1.Node) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, c.Name())

	if !nodeutils.IsManaged(node, c.cloudProvider) {
		return reconcile.Result{}, nil
	}
	condition := nodeutils.GetCondition(node, ConditionTypePreemptionScheduled)
	if condition.Status != corev1.ConditionTrue {
		return reconcile.Result{}, nil
	}

	nodeClaim, err := nodeutils.NodeClaimForNode(ctx, c.kubeClient, node)
	if err != nil {
		// A Node with no NodeClaim (or, ambiguously, more than one) is not ours to terminate. Both are
		// terminal for this Node until its NodeClaim mapping changes, which re-triggers the watch.
		return reconcile.Result{}, nodeutils.IgnoreDuplicateNodeClaimError(nodeutils.IgnoreNodeClaimNotFoundError(err))
	}
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("NodeClaim", klog.KObj(nodeClaim)))

	deadline := c.evictionDeadline(ctx, node, condition)
	if err := c.annotateTerminationTimestamp(ctx, nodeClaim, deadline); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	return reconcile.Result{}, c.deleteNodeClaim(ctx, node, nodeClaim, deadline)
}

// evictionDeadline resolves the instant Azure may start evicting the VM.
//
// A `PreemptionScheduled` condition is a statement that the VM is being reclaimed, not a suggestion. If
// the deadline cannot be recovered from the message, ignoring the notice would leave pods running on a
// node that is about to disappear without warning, so the controller fails closed and cleans up
// immediately. That is exactly the behavior Karpenter had before deadlines were understood, so the
// fallback can never be worse than the status quo -- but it is loud, because losing the deadline means
// losing the entire drain budget.
func (c *Controller) evictionDeadline(ctx context.Context, node *corev1.Node, condition corev1.NodeCondition) time.Time {
	notice, err := ParsePreemptionNotice(condition.Message)
	if err != nil {
		log.FromContext(ctx).Error(err, "unable to determine the azure spot eviction deadline, falling back to immediate node cleanup",
			"condition", ConditionTypePreemptionScheduled, "reason", condition.Reason)
		c.recorder.Publish(UnknownSpotEvictionDeadline(node, condition.Message))
		return c.clock.Now()
	}
	log.FromContext(ctx).V(1).Info("observed azure spot preemption notice",
		"eventID", notice.EventID, "notBefore", notice.NotBefore.Format(time.RFC3339),
		"remaining", notice.NotBefore.Sub(c.clock.Now()).Round(time.Second).String())
	return notice.NotBefore
}

// annotateTerminationTimestamp records the eviction deadline on the NodeClaim so that upstream's
// termination flow drains against Azure's clock instead of a synthesized one.
//
// The annotation only ever moves earlier. A deadline already at or before ours is either a shorter
// notice we have already handled or a terminationGracePeriod that expires sooner, and honoring the
// later of the two would let pods keep a grace period Azure will not respect.
func (c *Controller) annotateTerminationTimestamp(ctx context.Context, nodeClaim *karpv1.NodeClaim, deadline time.Time) error {
	terminationTime := deadline.UTC().Format(time.RFC3339)
	if existing, ok := nodeClaim.Annotations[karpv1.NodeClaimTerminationTimestampAnnotationKey]; ok {
		// An unparseable value is treated as absent: upstream cannot act on it either.
		if existingTime, err := time.Parse(time.RFC3339, existing); err == nil && !existingTime.After(deadline) {
			return nil
		}
	}
	stored := nodeClaim.DeepCopy()
	nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
		karpv1.NodeClaimTerminationTimestampAnnotationKey: terminationTime,
	})
	if equality.Semantic.DeepEqual(stored, nodeClaim) {
		return nil
	}
	// Optimistic locking: upstream's node.health and nodeclaim lifecycle controllers write the same
	// annotation. On conflict we requeue and re-evaluate against the freshly read NodeClaim rather than
	// clobbering a deadline that may be shorter than ours.
	if err := c.kubeClient.Patch(ctx, nodeClaim, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	log.FromContext(ctx).WithValues(karpv1.NodeClaimTerminationTimestampAnnotationKey, terminationTime).
		Info("annotated nodeclaim with the azure spot eviction deadline")
	return nil
}

// deleteNodeClaim hands the node to upstream's termination flow, which taints it unschedulable and
// drains it against the annotated deadline.
func (c *Controller) deleteNodeClaim(ctx context.Context, node *corev1.Node, nodeClaim *karpv1.NodeClaim, deadline time.Time) error {
	if !nodeClaim.DeletionTimestamp.IsZero() {
		// Already terminating; upstream owns the drain from here and re-deleting would be a no-op.
		return nil
	}
	if err := c.kubeClient.Delete(ctx, nodeClaim); err != nil {
		return client.IgnoreNotFound(err)
	}
	log.FromContext(ctx).Info("deleting nodeclaim preempted by azure spot eviction",
		"deadline", deadline.UTC().Format(time.RFC3339))
	c.recorder.Publish(SpotInterrupted(node, deadline))
	metrics.NodeClaimsDisruptedTotal.Inc(map[string]string{
		metrics.ReasonLabel:       SpotInterruptionReason,
		metrics.NodePoolLabel:     nodeClaim.Labels[karpv1.NodePoolLabelKey],
		metrics.CapacityTypeLabel: nodeClaim.Labels[karpv1.CapacityTypeLabelKey],
	})
	return nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(c.Name()).
		For(&corev1.Node{}, builder.WithPredicates(
			nodeutils.IsManagedPredicateFuncs(c.cloudProvider),
			preemptionScheduledPredicate(),
		)).
		WithOptions(controller.Options{
			RateLimiter: reasonable.RateLimiter(),
			// Mass Spot evictions arrive as a burst of independent Node updates, and every second of
			// queueing is a second of drain budget spent, so reconcile them in parallel.
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

// preemptionScheduledPredicate narrows the Node watch to meaningful changes of the PreemptionScheduled
// condition. The AKS node-problem-detector re-asserts an outstanding notice continuously, so without
// this every refresh would enqueue a redundant reconcile.
func preemptionScheduledPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// On (re)start every Node is replayed as a create, which is how an interrupted notice is
			// picked back up after a controller restart.
			return isPreemptionScheduled(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, oldOK := e.ObjectOld.(*corev1.Node)
			newNode, newOK := e.ObjectNew.(*corev1.Node)
			if !oldOK || !newOK {
				return false
			}
			newCondition := nodeutils.GetCondition(newNode, ConditionTypePreemptionScheduled)
			if newCondition.Status != corev1.ConditionTrue {
				return false
			}
			oldCondition := nodeutils.GetCondition(oldNode, ConditionTypePreemptionScheduled)
			// A changed message can carry an earlier deadline, so it is not redundant.
			return oldCondition.Status != newCondition.Status ||
				oldCondition.Message != newCondition.Message ||
				oldCondition.LastTransitionTime != newCondition.LastTransitionTime
		},
		DeleteFunc:  func(_ event.DeleteEvent) bool { return false },
		GenericFunc: func(_ event.GenericEvent) bool { return false },
	}
}

func isPreemptionScheduled(o client.Object) bool {
	node, ok := o.(*corev1.Node)
	if !ok {
		return false
	}
	return nodeutils.GetCondition(node, ConditionTypePreemptionScheduled).Status == corev1.ConditionTrue
}
