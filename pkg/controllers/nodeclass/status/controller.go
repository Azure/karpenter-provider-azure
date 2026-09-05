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

package status

import (
	"context"

	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"sigs.k8s.io/karpenter/pkg/utils/result"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/azclient/azapi"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/kubernetesversion"
	"github.com/awslabs/operatorpkg/reasonable"
)

type reconciler interface {
	Reconcile(context.Context, *v1beta1.AKSNodeClass) (reconcile.Result, error)
}

type Controller struct {
	kubeClient client.Client

	kubernetesVersion *KubernetesVersionReconciler
	nodeImage         *NodeImageReconciler
	subnet            *SubnetReconciler
	validation        *ValidationReconciler
	localDNS          *LocalDNSReconciler
}

// TODO: Consider splitting this (and other similar constructors)
// into some kind of builder struct to make the calling code easier to read.
func NewController(
	kubeClient client.Client,
	kubernetesVersionProvider kubernetesversion.KubernetesVersionProvider,
	nodeImageProvider imagefamily.NodeImageProvider,
	inClusterKubernetesInterface kubernetes.Interface,
	managedKubernetesInterface kubernetes.Interface,
	managedDynamicInterface dynamic.Interface,
	subnetClient azapi.SubnetsAPI,
	diskEncryptionSetsClient azapi.DiskEncryptionSetsAPI,
	parsedDiskEncryptionSetID *arm.ResourceID,
	instanceTypeProvider instancetype.Provider,
	networkPolicy string,
	networkPlugin string,
) *Controller {
	return &Controller{

		kubeClient: kubeClient,

		kubernetesVersion: NewKubernetesVersionReconciler(kubernetesVersionProvider),
		nodeImage:         NewNodeImageReconciler(nodeImageProvider, inClusterKubernetesInterface),
		subnet:            NewSubnetReconciler(subnetClient),
		validation:        NewValidationReconciler(diskEncryptionSetsClient, parsedDiskEncryptionSetID),
		localDNS:          NewLocalDNSReconciler(managedKubernetesInterface, managedDynamicInterface, kubeClient, instanceTypeProvider, networkPolicy, networkPlugin),
	}
}

func (c *Controller) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "nodeclass.status")

	if !controllerutil.ContainsFinalizer(nodeClass, v1beta1.TerminationFinalizer) {
		stored := nodeClass.DeepCopy()
		controllerutil.AddFinalizer(nodeClass, v1beta1.TerminationFinalizer)
		if err := c.kubeClient.Patch(ctx, nodeClass, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, err
		}
	}
	stored := nodeClass.DeepCopy()

	var results []reconcile.Result
	var errs error
	for _, reconciler := range []reconciler{
		c.kubernetesVersion,
		c.nodeImage,
		c.subnet,
		c.validation,
		c.localDNS,
	} {
		res, err := reconciler.Reconcile(ctx, nodeClass)
		errs = multierr.Append(errs, err)
		results = append(results, res)
	}

	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		// We use client.MergeFromWithOptimisticLock because patching a list with a JSON merge patch
		// can cause races due to the fact that it fully replaces the list on a change
		// Here, we are updating the status condition list
		if err := c.kubeClient.Status().Patch(ctx, nodeClass, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			errs = multierr.Append(errs, client.IgnoreNotFound(err))
		}
	}
	if errs != nil {
		return reconcile.Result{}, errs
	}
	return result.Min(results...), nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclass.status").
		For(&v1beta1.AKSNodeClass{}).
		// LocalDNS Mode=Preferred resolution reads the set of NodePools
		// referencing the AKSNodeClass (see checkInstanceTypeGate), so NodePool
		// churn is an input to this controller, not just AKSNodeClass churn.
		// Without this watch, a NodePool created alongside its AKSNodeClass
		// loses the race against the first reconcile, LocalDNS latches at
		// NoReferencingNodePools, and nothing re-runs the gate until the next
		// periodic requeue -- a multi-minute window in which nodes provision
		// with LocalDNS off.
		Watches(&karpv1.NodePool{}, handler.EnqueueRequestsFromMapFunc(nodePoolToAKSNodeClass)).
		WithOptions(controller.Options{
			RateLimiter: reasonable.RateLimiter(),
			// TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
			// Comments thread discussing this: https://github.com/Azure/karpenter-provider-azure/pull/729#discussion_r2006629809
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

// nodePoolToAKSNodeClass maps a NodePool to the AKSNodeClass it references so
// that creating, updating or deleting a NodePool re-runs the LocalDNS instance
// type gate against the current set of NodePools. NodePools that reference
// another provider's NodeClass map to nothing.
func nodePoolToAKSNodeClass(_ context.Context, o client.Object) []reconcile.Request {
	np, ok := o.(*karpv1.NodePool)
	if !ok {
		return nil
	}
	name, ok := aksNodeClassRefName(np)
	if !ok {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name}}}
}
