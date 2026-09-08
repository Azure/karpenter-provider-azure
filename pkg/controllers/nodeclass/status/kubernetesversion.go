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

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/kubernetesversion"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

const (
	kubernetesVersionReconcilerName = "nodeclass.kubernetesversion"
)

type KubernetesVersionReconciler struct {
	kubernetesVersionProvider kubernetesversion.KubernetesVersionProvider
	cm                        *pretty.ChangeMonitor
}

func NewKubernetesVersionReconciler(provider kubernetesversion.KubernetesVersionProvider) *KubernetesVersionReconciler {
	return &KubernetesVersionReconciler{
		kubernetesVersionProvider: provider,
		cm:                        pretty.NewChangeMonitor(),
	}
}

func (r *KubernetesVersionReconciler) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(kubernetesVersionReconcilerName).
		For(&v1beta1.AKSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter: reasonable.RateLimiter(),
			// TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
			// Comments thread discussing this: https://github.com/Azure/karpenter-provider-azure/pull/729#discussion_r2006629809
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), r))
}

// The kubernetes version reconciler will detect reasons to bump the kubernetes version:
//  1. Newly created AKSNodeClass, will select the version discovered from the API server
//  2. If a later kubernetes version is discovered from the API server, we will upgrade to it. [don't currently support rollback]
//     - Note: We will indirectly trigger an upgrade to latest image version as well, by resetting the Images readiness.
func (r *KubernetesVersionReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName(kubernetesVersionReconcilerName))
	logger := log.FromContext(ctx).WithValues("existingKubernetesVersion", nodeClass.Status.KubernetesVersion)

	goalK8sVersion, err := r.kubernetesVersionProvider.KubeServerVersion(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting kubernetes version, %w", err)
	}

	// Set controlPlaneVersion
	if nodeClass.Status.Versions == nil {
		nodeClass.Status.Versions = &v1beta1.VersionsStatus{}
	}
	nodeClass.Status.Versions.ControlPlaneKubernetesVersion = &goalK8sVersion

	var requestedVersion *string
	if nodeClass.Spec.Versions != nil && nodeClass.Spec.Versions.KubernetesVersion != nil {
		if err := validateVersion(*nodeClass.Spec.Versions.KubernetesVersion, goalK8sVersion); err != nil {
			return reconcile.Result{}, fmt.Errorf("validating requested kubernetes version, %w", err)
		}
		requestedVersion = nodeClass.Spec.Versions.KubernetesVersion
	}

	if requestedVersion != nil {
		logger = logger.WithValues("requestedKubernetesVersion", *requestedVersion)
		goalK8sVersion = *requestedVersion
	}

	// Handles case 1: init, update kubernetes status to API server version found
	if !nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady).IsTrue() || nodeClass.Status.KubernetesVersion == nil || *nodeClass.Status.KubernetesVersion == "" {
		logger.V(1).Info("init kubernetes version", "goalKubernetesVersion", goalK8sVersion)
	} else {
		// Check if there is an upgrade
		newK8sVersion, err := semver.Parse(goalK8sVersion)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("parsing discovered kubernetes version, %w", err)
		}
		currentK8sVersion, err := semver.Parse(*nodeClass.Status.KubernetesVersion)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("parsing current kubernetes version, %w", err)
		}

		if requestedVersion != nil {
			if !newK8sVersion.Equals(currentK8sVersion) {
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "KubernetesVersionChange", fmt.Sprintf("Kubernetes version changed from %s to %s", currentK8sVersion.String(), newK8sVersion.String()))
			}
		} else if newK8sVersion.GT(currentK8sVersion) {
			// Handles case 2: Upgrade kubernetes version [Note: we set node image to not ready, since we upgrade node image when there is a kubernetes upgrade]
			logger.V(1).Info("kubernetes upgrade detected", "currentKubernetesVersion", currentK8sVersion.String(), "discoveredKubernetesVersion", newK8sVersion.String())
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "KubernetesUpgrade", "Performing kubernetes upgrade, need to get latest images")
		} else if newK8sVersion.LT(currentK8sVersion) {
			logger.Info("detected potential kubernetes downgrade, keeping current version", "currentKubernetesVersion", currentK8sVersion.String(), "discoveredKubernetesVersion", newK8sVersion.String())
			// We do not currently support downgrading, so keep the kubernetes version the same
			goalK8sVersion = *nodeClass.Status.KubernetesVersion
		}
	}
	nodeClass.Status.KubernetesVersion = lo.ToPtr(goalK8sVersion)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
	if r.cm.HasChanged(fmt.Sprintf("nodeclass-%s-kubernetesversion", nodeClass.Name), nodeClass.Status.KubernetesVersion) {
		logger.WithValues("newKubernetesVersion", nodeClass.Status.KubernetesVersion).Info("new kubernetes version updated for nodeclass")
	}
	return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
}

/*
The requested version must satisfy AKS node/control-plane skew rules.

Notation:

C = cMajor.cMinor.cPatch is the control-plane version
N = nMajor.nMinor.nPatch is the requested node version
major versions must match (nMajor == cMajor)
node minor must not be greater than control-plane minor (nMinor <= cMinor)
node minor must be at most three minors behind control-plane minor (cMinor - nMinor <= 3)
when both are on the same minor, node patch must not be greater than control-plane patch (nMinor == cMinor -> nPatch <= cPatch)
Node Kubernetes version downgrade is allowed when selecting the retained image/Kubernetes rollback pair,
provided the retained Kubernetes version still satisfies these control-plane skew rules.
This changes only the node version; the control-plane version is not rolled back.
*/
func validateVersion(version, controlPlaneVersion string) error {
	versionSemver, err := semver.Parse(version)
	if err != nil {
		return fmt.Errorf("parsing kubernetes version, %w", err)
	}
	controlPlaneVersionSemver, err := semver.Parse(controlPlaneVersion)
	if err != nil {
		return fmt.Errorf("parsing control-plane kubernetes version, %w", err)
	}

	// major versions must match
	if versionSemver.Major != controlPlaneVersionSemver.Major {
		return fmt.Errorf("kubernetes version major mismatch: node %d vs control-plane %d", versionSemver.Major, controlPlaneVersionSemver.Major)
	}

	// node minor must not be greater than control-plane minor
	if versionSemver.Minor > controlPlaneVersionSemver.Minor {
		return fmt.Errorf("kubernetes version minor too new: node %d vs control-plane %d", versionSemver.Minor, controlPlaneVersionSemver.Minor)
	}

	// node minor must be at most three minors behind control-plane minor
	if controlPlaneVersionSemver.Minor-versionSemver.Minor > 3 {
		return fmt.Errorf("kubernetes version minor too old: node %d vs control-plane %d", versionSemver.Minor, controlPlaneVersionSemver.Minor)
	}

	// when both are on the same minor, node patch must not be greater than control-plane patch
	if versionSemver.Minor == controlPlaneVersionSemver.Minor && versionSemver.Patch > controlPlaneVersionSemver.Patch {
		return fmt.Errorf("kubernetes version patch too new: node %d vs control-plane %d", versionSemver.Patch, controlPlaneVersionSemver.Patch)
	}

	return nil
}
