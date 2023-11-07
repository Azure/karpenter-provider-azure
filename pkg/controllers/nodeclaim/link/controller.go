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

package link

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"

	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/metrics"
	"github.com/aws/karpenter-core/pkg/operator/controller"
	nodeclaimutil "github.com/aws/karpenter-core/pkg/utils/nodeclaim"
)

const creationReasonLabel = "linking"
const NodeClaimLinkedAnnotationKey = v1alpha5.MachineLinkedAnnotationKey // still using the one from v1alpha5

type Controller struct {
	kubeClient    client.Client
	cloudProvider *cloudprovider.CloudProvider
	Cache         *cache.Cache // exists due to eventual consistency on the controller-runtime cache
}

func NewController(kubeClient client.Client, cloudProvider *cloudprovider.CloudProvider) *Controller {
	return &Controller{
		kubeClient:    kubeClient,
		cloudProvider: cloudProvider,
		Cache:         cache.New(time.Minute, time.Second*10),
	}
}

func (c *Controller) Name() string {
	return "nodeclaim.link"
}

func (c *Controller) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	// We LIST VMs on the CloudProvider BEFORE we grab NodeClaims/Nodes on the cluster so that we make sure that, if
	// LISTing instances takes a long time, our information is more updated by the time we get to NodeClaim and Node LIST
	retrieved, err := c.cloudProvider.List(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing cloudprovider VMs, %w", err)
	}
	nodeClaims := &corev1beta1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaims); err != nil {
		return reconcile.Result{}, err
	}
	nodeList := &v1.NodeList{}
	if err := c.kubeClient.List(ctx, nodeList, client.HasLabels{corev1beta1.NodePoolLabelKey}); err != nil {
		return reconcile.Result{}, err
	}
	retrievedIDs := sets.New(lo.Map(retrieved, func(m *corev1beta1.NodeClaim, _ int) string { return m.Status.ProviderID })...)
	// Inject any nodes that are re-owned using karpenter.sh/nodepool but aren't found from the cloudprovider.List() call
	for i := range nodeList.Items {
		if _, ok := lo.Find(retrieved, func(r *corev1beta1.NodeClaim) bool {
			return retrievedIDs.Has(nodeList.Items[i].Spec.ProviderID)
		}); !ok {
			retrieved = append(retrieved, nodeclaimutil.NewFromNode(&nodeList.Items[i]))
		}
	}
	// Filter out any machines that shouldn't be linked
	retrieved = lo.Filter(retrieved, func(m *corev1beta1.NodeClaim, _ int) bool {
		return m.DeletionTimestamp.IsZero() && m.Labels[corev1beta1.NodePoolLabelKey] != ""
	})
	errs := make([]error, len(retrieved))
	workqueue.ParallelizeUntil(ctx, 100, len(retrieved), func(i int) {
		errs[i] = c.link(ctx, retrieved[i], nodeClaims.Items)
	})
	// Effectively, don't requeue this again once it succeeds
	return reconcile.Result{RequeueAfter: math.MaxInt64}, multierr.Combine(errs...)
}

func (c *Controller) link(ctx context.Context, retrieved *corev1beta1.NodeClaim, existingNodeClaims []corev1beta1.NodeClaim) error {
	ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("provider-id", retrieved.Status.ProviderID, "nodepool", retrieved.Labels[corev1beta1.NodePoolLabelKey]))
	nodePool := &corev1beta1.NodePool{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: retrieved.Labels[corev1beta1.NodePoolLabelKey]}, nodePool); err != nil {
		return client.IgnoreNotFound(err)
	}
	if c.shouldCreateLinkedNodeClaim(retrieved, existingNodeClaims) {
		// TODO v1beta1 there does not seem to be v1beta1 equivalent to this:
		// machine := machineutil.NewFromNode(machineutil.New(&v1.Node{}, provisioner))
		// For now creating manually
		nodeClaim := &corev1beta1.NodeClaim{
			Spec: nodePool.Spec.Template.Spec,
		}

		nodeClaim.GenerateName = fmt.Sprintf("%s-", nodePool.Name)
		// This annotation communicates to the nodeclaim controller that this is a nodeclaim linking scenario, not
		// a case where we want to provision a new VM
		nodeClaim.Annotations = lo.Assign(nodeClaim.Annotations, map[string]string{
			NodeClaimLinkedAnnotationKey: retrieved.Status.ProviderID,
		})
		if err := c.kubeClient.Create(ctx, nodeClaim); err != nil {
			return err
		}
		logging.FromContext(ctx).With("nodeclaim", nodeClaim.Name).Debugf("generated nodeclaim from cloudprovider")
		metrics.NodeClaimsCreatedCounter.With(prometheus.Labels{
			metrics.ReasonLabel:   creationReasonLabel,
			metrics.NodePoolLabel: nodeClaim.Labels[corev1beta1.NodePoolLabelKey],
		}).Inc()
		c.Cache.SetDefault(retrieved.Status.ProviderID, nil)
	}
	return corecloudprovider.IgnoreNodeClaimNotFoundError(c.cloudProvider.Link(ctx, retrieved))
}

func (c *Controller) shouldCreateLinkedNodeClaim(retrieved *corev1beta1.NodeClaim, existingNodeClaims []corev1beta1.NodeClaim) bool {
	// VM was already created but controller-runtime cache didn't update
	if _, ok := c.Cache.Get(retrieved.Status.ProviderID); ok {
		return false
	}
	// We have a nodeclaim registered for this, so no need to hydrate it
	if _, ok := lo.Find(existingNodeClaims, func(m corev1beta1.NodeClaim) bool {
		return m.Annotations[NodeClaimLinkedAnnotationKey] == retrieved.Status.ProviderID ||
			m.Status.ProviderID == retrieved.Status.ProviderID
	}); ok {
		return false
	}
	return true
}

func (c *Controller) Builder(_ context.Context, m manager.Manager) controller.Builder {
	return controller.NewSingletonManagedBy(m)
}
