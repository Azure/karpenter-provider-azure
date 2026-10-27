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

package interruption

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
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
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/events"
	"sigs.k8s.io/karpenter/pkg/metrics"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	nodeutils "sigs.k8s.io/karpenter/pkg/utils/node"
	nodeclaimutils "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"
)

type Controller struct {
	kubeClient    client.Client
	cloudProvider cloudprovider.CloudProvider
	recorder      events.Recorder
	clock         clock.Clock
}

func NewController(kubeClient client.Client, cloudProvider cloudprovider.CloudProvider, recorder events.Recorder, clk clock.Clock) *Controller {
	return &Controller{kubeClient: kubeClient, cloudProvider: cloudProvider, recorder: recorder, clock: clk}
}

func (c *Controller) Name() string {
	return "node.interruption"
}

func (c *Controller) Reconcile(ctx context.Context, node *corev1.Node) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, c.Name())
	if !nodeutils.IsManaged(node, c.cloudProvider) {
		return reconcile.Result{}, nil
	}
	condition := nodeutils.GetCondition(node, ConditionTypePreemptionScheduled)
	if condition.Status != corev1.ConditionTrue {
		return reconcile.Result{}, nil
	}
	deadline, preempt := c.evictionDeadline(ctx, node, condition.Message)
	if !preempt {
		return reconcile.Result{}, nil
	}
	nodeClaim, err := c.nodeClaimForNode(ctx, node)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("resolving nodeclaim for spot interruption, %w", err)
	}
	if nodeClaim == nil {
		return reconcile.Result{}, nil
	}
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("NodeClaim", client.ObjectKeyFromObject(nodeClaim)))

	timestamp, err := c.annotateDeadline(ctx, node, nodeClaim, deadline)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(fmt.Errorf("annotating spot termination deadline, %w", err))
	}
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}
	// Guard against deleting a recreated claim, or one whose deadline changed since the patch/read.
	if err := c.kubeClient.Delete(ctx, nodeClaim, client.Preconditions{UID: &nodeClaim.UID, ResourceVersion: &nodeClaim.ResourceVersion}); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(fmt.Errorf("deleting preempted nodeclaim, %w", err))
	}
	log.FromContext(ctx).Info("initiated spot interruption", "deadline", timestamp)
	c.publish(node, corev1.EventTypeWarning, "SpotInterrupted", fmt.Sprintf("Azure Spot preemption; draining against termination deadline %s", timestamp))
	metrics.NodeClaimsDisruptedTotal.Inc(map[string]string{
		metrics.ReasonLabel:       "spot_interruption",
		metrics.NodePoolLabel:     nodeClaim.Labels[karpv1.NodePoolLabelKey],
		metrics.CapacityTypeLabel: nodeClaim.Labels[karpv1.CapacityTypeLabelKey],
	})
	return reconcile.Result{}, nil
}

func (c *Controller) nodeClaimForNode(ctx context.Context, node *corev1.Node) (*karpv1.NodeClaim, error) {
	nodeClaim, err := nodeutils.NodeClaimForNode(ctx, c.kubeClient, node)
	if nodeutils.IsNodeClaimNotFoundError(err) {
		c.publish(node, corev1.EventTypeWarning, "SpotInterruptionMissingNodeClaim", "No matching NodeClaim for spot interruption; leaving node unchanged")
		return nil, nil
	}
	if nodeutils.IsDuplicateNodeClaimError(err) {
		return nil, fmt.Errorf("multiple nodeclaims match node, refusing spot interruption")
	}
	if err != nil {
		return nil, err
	}
	if !nodeclaimutils.IsManaged(nodeClaim, c.cloudProvider) {
		c.publish(node, corev1.EventTypeWarning, "SpotInterruptionUnmanagedNodeClaim", "Matching NodeClaim is not managed by this provider; leaving node unchanged")
		return nil, nil
	}
	return nodeClaim, nil
}

func (c *Controller) evictionDeadline(ctx context.Context, node *corev1.Node, message string) (time.Time, bool) {
	kind, deadline, err := parseNotice(message)
	switch kind {
	case advisoryNotice:
		c.publish(node, corev1.EventTypeNormal, "SpotRebalanceAdvisory", "Azure Spot rebalance advisory observed; leaving node running")
		return time.Time{}, false
	case startedNotice:
		return c.clock.Now(), true
	case scheduledNotice:
		if err != nil {
			// Only a positively identified mandatory Preempt may use the immediate-cleanup fallback.
			log.FromContext(ctx).Error(err, "spot preemption has no usable deadline, initiating immediate cleanup")
			c.publish(node, corev1.EventTypeWarning, "UnknownSpotEvictionDeadline", "Explicit Preempt Scheduled notice has no usable deadline; initiating immediate cleanup")
			return c.clock.Now(), true
		}
		return deadline, true
	default:
		c.publish(node, corev1.EventTypeWarning, "UnknownSpotInterruption", "Unrecognized PreemptionScheduled message; leaving node running until an explicit Preempt Scheduled or Started notice is received")
		return time.Time{}, false
	}
}

func (c *Controller) annotateDeadline(ctx context.Context, node *corev1.Node, nodeClaim *karpv1.NodeClaim, deadline time.Time) (string, error) {
	// Cap the first handoff before deletion; upstream lifecycle preserves an existing annotation.
	// Once deletion has started, use its timestamp rather than restarting the configured grace.
	if nodeClaim.Spec.TerminationGracePeriod != nil {
		start := c.clock.Now()
		if !nodeClaim.DeletionTimestamp.IsZero() {
			start = nodeClaim.DeletionTimestamp.Time
		}
		deadline = earlier(deadline, start.Add(nodeClaim.Spec.TerminationGracePeriod.Duration))
	}
	if value, ok := nodeClaim.Annotations[karpv1.NodeClaimTerminationTimestampAnnotationKey]; ok {
		existing, err := time.Parse(time.RFC3339, value)
		if err != nil {
			c.publish(node, corev1.EventTypeWarning, "InvalidSpotTerminationDeadline", "Replacing invalid NodeClaim termination timestamp with the spot eviction deadline")
		} else {
			deadline = earlier(deadline, existing)
		}
	}
	timestamp := deadline.UTC().Format(time.RFC3339)
	if nodeClaim.Annotations[karpv1.NodeClaimTerminationTimestampAnnotationKey] != timestamp {
		stored := nodeClaim.DeepCopy()
		nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{karpv1.NodeClaimTerminationTimestampAnnotationKey: timestamp})
		// Do not delete on a failed patch. A retry re-reads and preserves any earlier concurrent deadline.
		if err := c.kubeClient.Patch(ctx, nodeClaim, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			return "", err
		}
	}
	return timestamp, nil
}

func (c *Controller) publish(node *corev1.Node, eventType, reason, message string) {
	c.recorder.Publish(events.Event{
		InvolvedObject: node,
		Type:           eventType,
		Reason:         reason,
		Message:        message,
		DedupeValues:   []string{string(node.UID)},
	})
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(c.Name()).
		For(&corev1.Node{}, builder.WithPredicates(nodeutils.IsManagedPredicateFuncs(c.cloudProvider), noticePredicate())).
		Watches(&karpv1.NodeClaim{}, nodeutils.NodeClaimEventHandler(c.kubeClient)).
		WithOptions(controller.Options{RateLimiter: reasonable.RateLimiter(), MaxConcurrentReconciles: 10}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

func noticePredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return nodeutils.GetCondition(e.Object.(*corev1.Node), ConditionTypePreemptionScheduled).Status == corev1.ConditionTrue
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, newNode := e.ObjectOld.(*corev1.Node), e.ObjectNew.(*corev1.Node)
			oldCondition := nodeutils.GetCondition(oldNode, ConditionTypePreemptionScheduled)
			newCondition := nodeutils.GetCondition(newNode, ConditionTypePreemptionScheduled)
			// An advisory -> Preempt transition need not change status or LastTransitionTime.
			return newCondition.Status == corev1.ConditionTrue &&
				(oldCondition.Status != newCondition.Status || oldCondition.Message != newCondition.Message ||
					oldCondition.Reason != newCondition.Reason || oldNode.Spec.ProviderID != newNode.Spec.ProviderID ||
					!maps.Equal(oldNode.Labels, newNode.Labels))
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
