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

package remediation

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/samber/lo"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"

	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

const (
	azureCNIOverlayLabelKey = "kubernetes.azure.com/azure-cni-overlay"
)

// Controller adds azure-cni-overlay label to managed nodes that don't have it.
// It only runs (registers) when Azure CNI Overlay configuration is detected
type Controller struct {
	kubeClient client.Client
}

func NewController(kubeClient client.Client) *Controller {
	return &Controller{
		kubeClient: kubeClient,
	}
}

func (c *Controller) Reconcile(ctx context.Context, node *corev1.Node) (reconcile.Result, error) {
	if !isManagedWithoutLabel(node) { // extra guard and testability
		return reconcile.Result{}, nil
	}

	ctx = injection.WithControllerName(ctx, c.Name())
	stored := node.DeepCopy()
	node.Labels = lo.Assign(node.Labels, map[string]string{azureCNIOverlayLabelKey: "true"})
	if !equality.Semantic.DeepEqual(stored, node) {
		if err := c.kubeClient.Patch(ctx, node, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
	}
	return reconcile.Result{}, nil
}

func (c *Controller) Name() string {
	return "node.remediation"
}

func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	if !isAzureCNIOverlay(ctx) {
		return nil
	}

	return controllerruntime.NewControllerManagedBy(m).
		Named(c.Name()).
		For(&corev1.Node{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return isManagedWithoutLabel(o)
		}))).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

func isManagedWithoutLabel(o client.Object) bool {
	return o.GetLabels()[v1.NodePoolLabelKey] != "" && o.GetLabels()[azureCNIOverlayLabelKey] == ""
}

func isAzureCNIOverlay(ctx context.Context) bool {
	return options.FromContext(ctx).NetworkPlugin == consts.NetworkPluginAzure &&
		options.FromContext(ctx).NetworkPluginMode == consts.NetworkPluginModeOverlay
}
