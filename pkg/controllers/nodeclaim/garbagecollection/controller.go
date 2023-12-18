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

package garbagecollection

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"

	link "github.com/Azure/karpenter/pkg/controllers/nodeclaim/link"
	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/operator/controller"
)

type Controller struct {
	kubeClient      client.Client
	cloudProvider   *cloudprovider.CloudProvider
	successfulCount uint64 // keeps track of successful reconciles for more aggressive requeueing near the start of the controller
	linkController  *link.Controller
}

func NewController(kubeClient client.Client, cloudProvider *cloudprovider.CloudProvider, linkController *link.Controller) *Controller {
	return &Controller{
		kubeClient:      kubeClient,
		cloudProvider:   cloudProvider,
		successfulCount: 0,
		linkController:  linkController,
	}
}

func (c *Controller) Name() string {
	return "nodeclaim.garbagecollection"
}

func (c *Controller) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	// We LIST VMs on the CloudProvider BEFORE we grab NodeClaims/Nodes on the cluster so that we make sure that, if
	// LISTing instances takes a long time, our information is more updated by the time we get to nodeclaim and Node LIST
	// This works since our CloudProvider instances are deleted based on whether the NodeClaim exists or not, not vice-versa
	retrieved, err := c.cloudProvider.List(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing cloudprovider VMs, %w", err)
	}
	managedRetrieved := lo.Filter(retrieved, func(m *corev1beta1.NodeClaim, _ int) bool {
		return m.DeletionTimestamp.IsZero()
	})
	nodeClaims := &corev1beta1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaims); err != nil {
		return reconcile.Result{}, err
	}
	nodeList := &v1.NodeList{}
	if err := c.kubeClient.List(ctx, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	resolvedNodeClaims := lo.Filter(nodeClaims.Items, func(m corev1beta1.NodeClaim, _ int) bool {
		return m.Status.ProviderID != "" || m.Annotations[v1alpha2.NodeClaimLinkedAnnotationKey] != ""
	})
	resolvedProviderIDs := sets.New[string](lo.Map(resolvedNodeClaims, func(m corev1beta1.NodeClaim, _ int) string {
		if m.Status.ProviderID != "" {
			return m.Status.ProviderID
		}
		return m.Annotations[v1alpha2.NodeClaimLinkedAnnotationKey]
	})...)
	errs := make([]error, len(retrieved))
	workqueue.ParallelizeUntil(ctx, 100, len(managedRetrieved), func(i int) {
		_, recentlyLinked := c.linkController.Cache.Get(managedRetrieved[i].Status.ProviderID)

		if !recentlyLinked &&
			!resolvedProviderIDs.Has(managedRetrieved[i].Status.ProviderID) &&
			time.Since(managedRetrieved[i].CreationTimestamp.Time) > time.Minute*5 {
			errs[i] = c.garbageCollect(ctx, managedRetrieved[i], nodeList)
		}
	})
	c.successfulCount++
	return reconcile.Result{RequeueAfter: lo.Ternary(c.successfulCount <= 20, time.Second*10, time.Minute*2)}, multierr.Combine(errs...)
}

func (c *Controller) garbageCollect(ctx context.Context, nodeClaim *corev1beta1.NodeClaim, nodeList *v1.NodeList) error {
	ctx = logging.WithLogger(ctx, logging.FromContext(ctx).With("provider-id", nodeClaim.Status.ProviderID))
	if err := c.cloudProvider.Delete(ctx, nodeClaim); err != nil {
		return corecloudprovider.IgnoreNodeClaimNotFoundError(err)
	}
	logging.FromContext(ctx).Debugf("garbage collected cloudprovider instance")

	// Go ahead and cleanup the node if we know that it exists to make scheduling go quicker
	if node, ok := lo.Find(nodeList.Items, func(n v1.Node) bool {
		return n.Spec.ProviderID == nodeClaim.Status.ProviderID
	}); ok {
		if err := c.kubeClient.Delete(ctx, &node); err != nil {
			return client.IgnoreNotFound(err)
		}
		logging.FromContext(ctx).With("node", node.Name).Debugf("garbage collected node")
	}
	return nil
}

func (c *Controller) Builder(_ context.Context, m manager.Manager) controller.Builder {
	return controller.NewSingletonManagedBy(m)
}
