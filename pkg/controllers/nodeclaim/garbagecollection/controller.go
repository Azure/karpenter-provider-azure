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
	"sync"
	"time"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/awslabs/operatorpkg/singleton"

	// "github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"knative.dev/pkg/logging"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

type Controller struct {
	kubeClient       client.Client
	cloudProvider    corecloudprovider.CloudProvider
	instanceProvider instance.Provider
	successfulCount  uint64 // keeps track of successful reconciles for more aggressive requeueing near the start of the controller
}

func NewController(kubeClient client.Client, cloudProvider corecloudprovider.CloudProvider, instanceProvider instance.Provider) *Controller {
	return &Controller{
		kubeClient:       kubeClient,
		cloudProvider:    cloudProvider,
		instanceProvider: instanceProvider,
		successfulCount:  0,
	}
}

func (c *Controller) Reconcile(ctx context.Context) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "instance.garbagecollection")
	var aggregatedError error

	// Perform VM garbage collection
	if err := c.gcVMs(ctx); err != nil {
		aggregatedError = multierr.Append(aggregatedError, fmt.Errorf("VM garbage collection failed: %w", err))
	}

	// Perform NIC garbage collection
	if err := c.gcNics(ctx); err != nil {
		aggregatedError = multierr.Append(aggregatedError, fmt.Errorf("NIC garbage collection failed: %w", err))
	}

	c.successfulCount++

	return reconcile.Result{
		RequeueAfter: lo.Ternary(c.successfulCount <= 20, 10*time.Second, 2*time.Minute),
	}, aggregatedError
}

// gcVMs handles the garbage collection of virtual machines.
func (c *Controller) gcVMs(ctx context.Context) error {
	// List VMs from the CloudProvider
	retrieved, err := c.cloudProvider.List(ctx)
	if err != nil {
		return fmt.Errorf("listing cloudprovider VMs: %w", err)
	}

	// Filter out VMs that are marked for deletion
	managedRetrieved := lo.Filter(retrieved, func(nc *karpv1.NodeClaim, _ int) bool {
		return nc.DeletionTimestamp.IsZero()
	})

	// List NodeClaims and Nodes from the cluster
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return fmt.Errorf("listing NodeClaims: %w", err)
	}

	nodeList := &v1.NodeList{}
	if err := c.kubeClient.List(ctx, nodeList); err != nil {
		return fmt.Errorf("listing Nodes: %w", err)
	}

	resolvedProviderIDs := sets.New[string](lo.FilterMap(nodeClaimList.Items, func(n karpv1.NodeClaim, _ int) (string, bool) {
		return n.Status.ProviderID, n.Status.ProviderID != ""
	})...)

	errs := make([]error, len(managedRetrieved))

	workqueue.ParallelizeUntil(ctx, 100, len(managedRetrieved), func(i int) {
		vm := managedRetrieved[i]
		if !resolvedProviderIDs.Has(vm.Status.ProviderID) &&
			time.Since(vm.CreationTimestamp.Time) > 5*time.Minute {
			errs[i] = c.garbageCollect(ctx, vm, nodeList)
		}
	})

	if combinedErr := multierr.Combine(errs...); combinedErr != nil {
		return combinedErr
	}

	return nil
}

// gcNics handles the garbage collection of network interfaces.
func (c *Controller) gcNics(ctx context.Context) error {
	// Refresh the list of NodeClaims after VM GC
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return fmt.Errorf("listing NodeClaims for NIC GC: %w", err)
	}

	// Normalize NodeClaim names to match NIC naming conventions
	nodeClaimNames := sets.New[string]()
	for _, nodeClaim := range nodeClaimList.Items {
		// Adjust the prefix as per the aks naming convention
		nodeClaimNames.Insert(fmt.Sprintf("aks-%s", nodeClaim.Name))
	}

	// List all NICs from the instance provider, this List call will give us network interfaces that belong to karpenter
	nics, err := c.instanceProvider.ListNics(ctx)
	if err != nil {
		return fmt.Errorf("listing NICs: %w", err)
	}

	// Initialize a slice to collect errors from goroutines
	var gcErrors []error
	var mu sync.Mutex

	// Parallelize the garbage collection process for NICs
	workqueue.ParallelizeUntil(ctx, 100, len(nics), func(i int) {
		nicName := lo.FromPtr(nics[i].Name)
		if !nodeClaimNames.Has(nicName) {
			if err := c.instanceProvider.Delete(ctx, nicName); err != nil {
				mu.Lock()
				gcErrors = append(gcErrors, fmt.Errorf("deleting NIC %s: %w", nicName, err))
				mu.Unlock()
			}
			logging.FromContext(ctx).With("nic", nicName).Infof("garbage collected NIC")
		}
	})

	// Combine all errors into one
	if len(gcErrors) > 0 {
		return multierr.Combine(gcErrors...)
	}

	return nil
}

func (c *Controller) garbageCollect(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeList *v1.NodeList) error {
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

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("instance.garbagecollection").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
