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

package inplaceupdate

import (
	"context"
	"fmt"
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
	corenodeclaimutils "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

type Controller struct {
	kubeClient         client.Client
	vmInstanceProvider instance.VMProvider
}

func NewController(
	kubeClient client.Client,
	vmInstanceProvider instance.VMProvider,
) *Controller {
	return &Controller{
		kubeClient:         kubeClient,
		vmInstanceProvider: vmInstanceProvider,
	}
}

func (c *Controller) Reconcile(ctx context.Context, nodeClaim *karpv1.NodeClaim) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "nodeclaim.inplaceupdate")
	// No need to add nodeClaim name to the context as it's already there

	// Get the NodeClass
	nodeClass, err := nodeclaimutils.GetAKSNodeClass(ctx, c.kubeClient, nodeClaim)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("resolving AKSNodeClass, %w", err)
	}

	// TODO: When we have sources of truth coming from NodePool we can do:
	// nodePool, err := nodeclaimutil.Owner(ctx, c.kubeClient, nodeClaim)
	// TODO: To look it up and use that as input to calculate the goal state as well

	// Compare the expected hash with the actual hash
	options := options.FromContext(ctx)
	goalHash, err := HashFromNodeClaim(options, nodeClaim, nodeClass)
	if err != nil {
		return reconcile.Result{}, err
	}
	actualHash := nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]

	log.FromContext(ctx).V(1).Info("comparing in-place update hashes", "goalHash", goalHash, "actualHash", actualHash)

	// If there's no difference from goal state, no need to do anything else
	if goalHash == actualHash {
		return reconcile.Result{}, nil
	}

	if shouldProcess, result := c.shouldProcess(ctx, nodeClaim); !shouldProcess {
		return result, nil
	}

	stored := nodeClaim.DeepCopy()

	vm, err := nodeclaimutils.GetVM(ctx, c.vmInstanceProvider, nodeClaim)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting VM for nodeClaim %s: %w", nodeClaim.Name, err)
	}

	err = c.applyPatch(ctx, options, nodeClaim, nodeClass, vm)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("applying patch to VM for nodeClaim %s: %w", nodeClaim.Name, err)
	}

	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	// Regardless of whether we actually changed anything in Azure, we have confirmed that
	// the goal shape is in alignment with our expected shape, so update the annotation to reflect that
	nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash] = goalHash
	err = c.kubeClient.Patch(ctx, nodeClaim, client.MergeFrom(stored))
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	log.FromContext(ctx).V(1).Info("successfully saved new in-place update hash", "goalHash", goalHash)

	return reconcile.Result{}, nil
}

func (c *Controller) shouldProcess(ctx context.Context, nodeClaim *karpv1.NodeClaim) (bool, reconcile.Result) {
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return false, reconcile.Result{}
	}

	// If the node isn't registered yet, we need to wait until it is as otherwise all the resources we need to update may not exist yet
	if !nodeClaim.StatusConditions().Get(karpv1.ConditionTypeRegistered).IsTrue() {
		log.FromContext(ctx).V(1).Info("can't update yet as the claim is not registered")
		return false, reconcile.Result{RequeueAfter: 60 * time.Second}
	}

	if nodeClaim.Status.ProviderID == "" {
		log.FromContext(ctx).V(1).Info("can't update yet as there's no provider ID")
		return false, reconcile.Result{RequeueAfter: 60 * time.Second}
	}

	return true, reconcile.Result{}
}

func (c *Controller) applyPatch(
	ctx context.Context,
	options *options.Options,
	nodeClaim *karpv1.NodeClaim,
	nodeClass *v1beta1.AKSNodeClass,
	vm *armcompute.VirtualMachine,
) error {
	update := CalculateVMPatch(options, nodeClaim, nodeClass, vm)
	// This is safe only as long as we're not updating fields which we consider secret.
	// If we do/are, we need to redact them.
	logVMPatch(ctx, update)

	// Apply the update, if one is needed
	if update != nil {
		err := c.vmInstanceProvider.Update(ctx, lo.FromPtr(vm.Name), *update)
		if err != nil {
			return fmt.Errorf("failed to apply update to VM, %w", err)
		}
	}

	return nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclaim.inplaceupdate").
		For(
			&karpv1.NodeClaim{},
			builder.WithPredicates(
				predicate.Or(
					predicate.GenerationChangedPredicate{}, // Note that this will trigger on pod restart for all Machines.
				),
			)).
		Watches(&v1beta1.AKSNodeClass{}, corenodeclaimutils.NodeClassEventHandler(m.GetClient()), builder.WithPredicates(tagsChangedPredicate{})).
		// TODO: Can add .Watches(&karpv1.NodePool{}, nodeclaimutil.NodePoolEventHandler(c.kubeClient))
		// TODO: similar to https://github.com/kubernetes-sigs/karpenter/blob/main/pkg/controllers/nodeclaim/disruption/controller.go#L214C3-L217C5
		// TODO: if/when we need to monitor provisioner changes and flow updates on the NodePool down to the underlying VMs.
		WithOptions(controller.Options{
			RateLimiter: reasonable.RateLimiter(),
			// TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
			// Comments thread discussing this: https://github.com/Azure/karpenter-provider-azure/pull/729#discussion_r2006629809
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}
