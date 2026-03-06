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

	"k8s.io/apimachinery/pkg/api/equality"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/awslabs/operatorpkg/reasonable"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AzureNodeClassController is a minimal status controller for AzureNodeClass.
// Unlike the AKSNodeClass status controller, this skips AKS-specific reconcilers
// (kubernetes version, node images, subnet). It only performs basic validation
// and sets the Ready condition.
type AzureNodeClassController struct {
	kubeClient client.Client
}

func NewAzureNodeClassController(kubeClient client.Client) *AzureNodeClassController {
	return &AzureNodeClassController{
		kubeClient: kubeClient,
	}
}

func (c *AzureNodeClassController) Reconcile(ctx context.Context, nodeClass *v1alpha1.AzureNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "azurenodeclass.status")

	if !controllerutil.ContainsFinalizer(nodeClass, v1beta1.TerminationFinalizer) {
		stored := nodeClass.DeepCopy()
		controllerutil.AddFinalizer(nodeClass, v1beta1.TerminationFinalizer)
		if err := c.kubeClient.Patch(ctx, nodeClass, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, err
		}
	}
	stored := nodeClass.DeepCopy()

	// For AzureNodeClass, validation is minimal — just set ValidationSucceeded=True.
	// The user controls bootstrapping (userData, imageID) and we don't need AKS-specific
	// status resolution (kubernetes version, images, subnet provider).
	nodeClass.StatusConditions().SetTrue(v1alpha1.ConditionTypeValidationSucceeded)

	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		if err := c.kubeClient.Status().Patch(ctx, nodeClass, client.MergeFromWithOptions(stored, client.MergeFromWithOptimisticLock{})); err != nil {
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
	}

	return reconcile.Result{}, nil
}

func (c *AzureNodeClassController) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("azurenodeclass.status").
		For(&v1alpha1.AzureNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}
