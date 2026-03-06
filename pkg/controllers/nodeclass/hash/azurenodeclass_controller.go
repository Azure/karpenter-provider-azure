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

package hash

import (
	"context"

	"github.com/samber/lo"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/equality"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/operator/injection"

	"github.com/awslabs/operatorpkg/reasonable"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha1"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

// AzureNodeClassController is the hash controller for AzureNodeClass.
// It mirrors the AKSNodeClass hash controller but operates on AzureNodeClass objects.
// The hash is computed via the adapter (AKSNodeClassFromAzureNodeClass) so that drift
// detection works correctly — the same hash algorithm is used at creation time
// (via setAdditionalAnnotationsForNewNodeClaim) and here.
type AzureNodeClassController struct {
	kubeClient client.Client
}

func NewAzureNodeClassController(kubeClient client.Client) *AzureNodeClassController {
	return &AzureNodeClassController{
		kubeClient: kubeClient,
	}
}

func (c *AzureNodeClassController) Reconcile(ctx context.Context, nodeClass *v1alpha1.AzureNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "azurenodeclass.hash")

	stored := nodeClass.DeepCopy()

	// Compute the hash via the adapter so it matches what setAdditionalAnnotationsForNewNodeClaim writes on NodeClaims.
	adapted := nodeclaimutils.AKSNodeClassFromAzureNodeClass(nodeClass)
	adaptedHash := adapted.Hash()

	if nodeClass.Annotations[v1beta1.AnnotationAKSNodeClassHashVersion] != v1beta1.AKSNodeClassHashVersion {
		if err := c.updateNodeClaimHash(ctx, nodeClass, adaptedHash); err != nil {
			return reconcile.Result{}, err
		}
	}
	nodeClass.Annotations = lo.Assign(nodeClass.Annotations, map[string]string{
		v1beta1.AnnotationAKSNodeClassHash:        adaptedHash,
		v1beta1.AnnotationAKSNodeClassHashVersion: v1beta1.AKSNodeClassHashVersion,
	})

	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		if err := c.kubeClient.Patch(ctx, nodeClass, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *AzureNodeClassController) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("azurenodeclass.hash").
		For(&v1alpha1.AzureNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

// updateNodeClaimHash updates the hash annotations on all NodeClaims referencing this AzureNodeClass
// when the hash version changes (indicating a breaking change in the hash algorithm).
func (c *AzureNodeClassController) updateNodeClaimHash(ctx context.Context, nodeClass *v1alpha1.AzureNodeClass, adaptedHash string) error {
	ncList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, ncList, client.MatchingFields{"spec.nodeClassRef.name": nodeClass.Name}); err != nil {
		return err
	}

	errs := make([]error, len(ncList.Items))
	for i := range ncList.Items {
		nc := ncList.Items[i]
		stored := nc.DeepCopy()

		if nc.Annotations[v1beta1.AnnotationAKSNodeClassHashVersion] != v1beta1.AKSNodeClassHashVersion {
			nc.Annotations = lo.Assign(nc.Annotations, map[string]string{
				v1beta1.AnnotationAKSNodeClassHashVersion: v1beta1.AKSNodeClassHashVersion,
			})

			// Any NodeClaim that is already drifted will remain drifted if the hash version doesn't match.
			// Since the hashing mechanism has changed we will not be able to determine if the drifted status has changed.
			if nc.StatusConditions().Get(karpv1.ConditionTypeDrifted) == nil {
				nc.Annotations = lo.Assign(nc.Annotations, map[string]string{
					v1beta1.AnnotationAKSNodeClassHash: adaptedHash,
				})
			}

			if !equality.Semantic.DeepEqual(stored, nc) {
				if err := c.kubeClient.Patch(ctx, &nc, client.MergeFrom(stored)); err != nil {
					errs[i] = client.IgnoreNotFound(err)
				}
			}
		}
	}

	return multierr.Combine(errs...)
}
