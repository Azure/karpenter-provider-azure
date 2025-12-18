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

	"github.com/awslabs/operatorpkg/singleton"

	"github.com/samber/lo"
	"go.uber.org/multierr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type VirtualMachine struct {
	kubeClient      client.Client
	cloudProvider   corecloudprovider.CloudProvider
	successfulCount uint64 // keeps track of successful reconciles for more aggressive requeuing near the start of the controller
}

func NewVirtualMachine(kubeClient client.Client, cloudProvider corecloudprovider.CloudProvider) *VirtualMachine {
	return &VirtualMachine{
		kubeClient:      kubeClient,
		cloudProvider:   cloudProvider,
		successfulCount: 0,
	}
}

func (c *VirtualMachine) Reconcile(ctx context.Context) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "instance.garbagecollection")

	// We LIST VMs on the CloudProvider BEFORE we grab NodeClaims/Nodes on the cluster so that we make sure that, if
	// LISTing instances takes a long time, our information is more updated by the time we get to nodeclaim and Node LIST
	// This works since our CloudProvider instances are deleted based on whether the NodeClaim exists or not, not vice-versa
	cloudNodeClaims, err := c.cloudProvider.List(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing cloudprovider VMs, %w", err)
	}

	cloudNodeClaims = lo.Filter(cloudNodeClaims, func(nc *karpv1.NodeClaim, _ int) bool {
		return nc.DeletionTimestamp.IsZero()
	})
	clusterNodeClaims := &karpv1.NodeClaimList{}
	if err = c.kubeClient.List(ctx, clusterNodeClaims); err != nil {
		return reconcile.Result{}, err
	}
	nodeList := &v1.NodeList{}
	if err := c.kubeClient.List(ctx, nodeList); err != nil {
		return reconcile.Result{}, err
	}
	clusterProviderIDs := sets.New[string](lo.FilterMap(clusterNodeClaims.Items, func(n karpv1.NodeClaim, _ int) (string, bool) {
		return n.Status.ProviderID, n.Status.ProviderID != ""
	})...)
	errs := make([]error, len(cloudNodeClaims))
	workqueue.ParallelizeUntil(ctx, 100, len(cloudNodeClaims), func(i int) {
		// Garbage collect if the cloud instance has been around for more than 5 minutes, yet still no matching (per ProviderID) cluster NodeClaim.
		// Note that the "match" occurs after cloudprovider.Create() returns and cluster NodeClaim ProviderID is populated as a result.
		// Although, the intention of garbage collection is to clear instances with missing/deleted NodeClaim.
		// This 5m is more of a grace period for newly-created instances that have yet to populate NodeClaim after.
		if !clusterProviderIDs.Has(cloudNodeClaims[i].Status.ProviderID) &&
			time.Since(cloudNodeClaims[i].CreationTimestamp.Time) > time.Minute*5 {
			errs[i] = c.garbageCollect(ctx, cloudNodeClaims[i], nodeList)
			// In the case that CreationTimestamp is irretrievable (technically, when CreationTimestamp = 0 = epoch), grace period will effectively be disabled.
			// Which could be dangerous if the instance is legitimately awaiting NodeClaim population.
		}
	})
	if err = multierr.Combine(errs...); err != nil {
		return reconcile.Result{}, err
	}
	c.successfulCount++
	return reconcile.Result{RequeueAfter: lo.Ternary(c.successfulCount <= 20, time.Second*10, time.Minute*2)}, nil
}

func (c *VirtualMachine) garbageCollect(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeList *v1.NodeList) error {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues("providerID", nodeClaim.Status.ProviderID))
	if err := c.cloudProvider.Delete(ctx, nodeClaim); err != nil {
		return corecloudprovider.IgnoreNodeClaimNotFoundError(err)
	}
	log.FromContext(ctx).V(1).Info("garbage collected cloudprovider instance")

	// Go ahead and cleanup the node if we know that it exists to make scheduling go quicker
	if node, ok := lo.Find(nodeList.Items, func(n v1.Node) bool {
		return n.Spec.ProviderID == nodeClaim.Status.ProviderID
	}); ok {
		if err := c.kubeClient.Delete(ctx, &node); err != nil {
			return client.IgnoreNotFound(err)
		}
		log.FromContext(ctx).V(1).Info("garbage collected node", "Node", node.Name)
	}
	return nil
}

func (c *VirtualMachine) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("instance.garbagecollection").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
