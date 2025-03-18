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
	"fmt"

	"knative.dev/pkg/logging"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/blang/semver/v4"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

type KubernetesVersionReconciler struct {
	kubernetesVersionProvider imagefamily.KubernetesVersionProvider
	cm                        *pretty.ChangeMonitor
}

func NeKubernetesVersionReconciler(provider imagefamily.KubernetesVersionProvider) *KubernetesVersionReconciler {
	return &KubernetesVersionReconciler{
		kubernetesVersionProvider: provider,
		cm:                        pretty.NewChangeMonitor(),
	}
}

func (r *KubernetesVersionReconciler) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclass.kubernetesversion").
		For(&v1alpha2.AKSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), r))
}

// The kubernetes version reconciler will detect reasons to bump the kubernetes version:
//  1. Newly created AKSNodeClass, will select the version discovered from the API server
//  2. If a later kubernetes version is discovered from the API server, we will upgrade to it. [don't currently support rollback]
//     - Note: We will indirectly trigger an upgrade to latest image version as well, by resetting the NodeImage readiness.
func (r *KubernetesVersionReconciler) Reconcile(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (reconcile.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Debug("nodeclass.kubernetesversion: starting reconcile")

	k8sVersion, err := r.kubernetesVersionProvider.KubeServerVersion(ctx)
	if err != nil {
		logger.Debug("nodeclass.kubernetesversion: err getting kubernetes version")
		return reconcile.Result{}, fmt.Errorf("getting kubernetes version, %w", err)
	}

	// Case 1: init, update kubernetes status to API server version found
	if !nodeClass.StatusConditions().Get(v1alpha2.ConditionTypeKubernetesVersionReady).IsTrue() || nodeClass.Status.KubernetesVersion == "" {
		logger.Debug("nodeclass.kubernetesversion: init kubernetes version")
		nodeClass.Status.KubernetesVersion = k8sVersion
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)
	} else {
		// Check if there is an upgrade
		newK8sVersion, err := semver.Parse(k8sVersion)
		if err != nil {
			logger.Debug("nodeclass.kubernetesversion: err parsing new kubernetes version")
			return reconcile.Result{}, fmt.Errorf("parsing discovered kubernetes version, %w", err)
		}
		currentK8sVersion, err := semver.Parse(nodeClass.Status.KubernetesVersion)
		if err != nil {
			logger.Debug("nodeclass.kubernetesversion: err parsing current kubernetes version")
			return reconcile.Result{}, fmt.Errorf("parsing current kubernetes version, %w", err)
		}
		// Case 2: Upgrade kubernetes version [Note: we set node image to not ready, since we upgrade node image when there is a kubernetes upgrade]
		if newK8sVersion.GT(currentK8sVersion) {
			logger.Debug("nodeclass.kubernetesversion: kubernetes upgrade detected")
			nodeClass.Status.KubernetesVersion = k8sVersion
			nodeClass.StatusConditions().SetFalse(v1alpha2.ConditionTypeNodeImageReady, "KubernetesUpgrade", "Performing kubernetes upgrade, need to get latest node images")
			nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeKubernetesVersionReady)
		}
	}
	logger.Debug("nodeclass.kubernetesversion: successful reconcile")
	return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
}
