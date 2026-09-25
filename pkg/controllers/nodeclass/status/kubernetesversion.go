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
//
//nolint:gocyclo // Keep pinned and automatic Kubernetes version status ownership in this reconciler.
func (r *KubernetesVersionReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName(kubernetesVersionReconcilerName))
	logger := log.FromContext(ctx).WithValues("existingKubernetesVersion", nodeClass.Status.KubernetesVersion)

	controlPlaneVersion, err := r.kubernetesVersionProvider.KubeServerVersion(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting control plane kubernetes version, %w", err)
	}

	if nodeClass.Status.ObservedVersions == nil {
		nodeClass.Status.ObservedVersions = &v1beta1.ObservedVersionsStatus{}
	}
	nodeClass.Status.ObservedVersions.ControlPlaneKubernetesVersion = &controlPlaneVersion

	goalK8sVersion := controlPlaneVersion
	if reqImgVer, reqK8sVer := requestedVersions(nodeClass); reqK8sVer != "" {
		// Handles case 0: requested Kubernetes version is pinned
		valid, err := r.validatePinnedK8sVersion(ctx, nodeClass, reqK8sVer, reqImgVer, goalK8sVersion)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !valid {
			return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
		}
		goalK8sVersion = reqK8sVer
	} else if !nodeClass.StatusConditions().Get(v1beta1.ConditionTypeKubernetesVersionReady).IsTrue() || nodeClass.Status.KubernetesVersion == nil || *nodeClass.Status.KubernetesVersion == "" {
		// Handles case 1: init, update kubernetes status to API server version found
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
		// Handles case 2: Upgrade kubernetes version [Note: we set node image to not ready, since we upgrade node image when there is a kubernetes upgrade]
		if newK8sVersion.GT(currentK8sVersion) {
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

// validatePinnedK8sVersion validates a requested Kubernetes version and updates the corresponding
// NodeClass readiness conditions. Operational errors are returned.
func (r *KubernetesVersionReconciler) validatePinnedK8sVersion(ctx context.Context, nodeClass *v1beta1.AKSNodeClass, reqK8sVer, reqImgVer, controlPlaneVersion string) (bool, error) {
	versionSemver, err := semver.Parse(reqK8sVer)
	if err != nil {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionInvalidFormat", fmt.Sprintf("invalid kubernetes version format: %v", err))
		return false, nil
	}
	controlPlaneVersionSemver, err := semver.Parse(controlPlaneVersion)
	if err != nil {
		return false, fmt.Errorf("parsing control-plane kubernetes version: %w", err)
	}

	if !validateKubernetesVersionSkew(nodeClass, versionSemver, controlPlaneVersionSemver) {
		return false, nil
	}

	// Check that this exists
	supported, err := r.kubernetesVersionProvider.IsSupported(ctx, reqK8sVer)
	if err != nil {
		return false, fmt.Errorf("checking if kubernetes version is supported: %w", err)
	}
	if !supported {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionUnsupported", fmt.Sprintf("kubernetes version %s is not supported", reqK8sVer))
		return false, nil
	}

	currentK8sVersion := lo.FromPtr(nodeClass.Status.KubernetesVersion)
	if !validateCurrentOrLatestImagePin(nodeClass, currentK8sVersion, reqK8sVer, reqImgVer) {
		return false, nil
	}

	if currentK8sVersion != reqK8sVer {
		log.FromContext(ctx).V(1).Info("requested Kubernetes version differs from current version", "currentKubernetesVersion", currentK8sVersion, "requestedKubernetesVersion", reqK8sVer)
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "KubernetesPinning", "Performing kubernetes version change, need to get latest images")
	}

	return true, nil
}

// We consider the pinning valid if the requested image version matches either the current or latest image version,
// and the requested Kubernetes version matches the current Kubernetes version. For these two cases, the requested Kubernetes version
// must match the current Kubernetes version or be a valid rollback, otherwise the pinning is considered invalid because we cannot
// determine if the requested image and Kubernetes version combination is valid.
func validateCurrentOrLatestImagePin(nodeClass *v1beta1.AKSNodeClass, currentK8sVersion, reqK8sVer, reqImgVer string) bool {
	currentImageVersion := ""
	if len(nodeClass.Status.Images) > 0 {
		currentImageVersion = parseVersion(nodeClass.Status.Images[0].ID)
	}
	latestImageVersion := lo.FromPtr(nodeClass.Status.ObservedVersions.LatestImageVersion)

	kubernetesVersionChanging := currentK8sVersion != "" && currentK8sVersion != reqK8sVer
	pinningCurrentOrLatestImage := reqImgVer != "" &&
		(reqImgVer == currentImageVersion || reqImgVer == latestImageVersion)

	if kubernetesVersionChanging && pinningCurrentOrLatestImage {
		nodeClass.StatusConditions().SetFalse(
			v1beta1.ConditionTypeKubernetesVersionReady,
			"KubernetesVersionImagePinIncompatible",
			"cannot change Kubernetes version while pinning to the current or latest node image version",
		)
		return false
	}

	return true
}

func validateKubernetesVersionSkew(nodeClass *v1beta1.AKSNodeClass, version, controlPlaneVersion semver.Version) bool {
	// major versions must match
	if version.Major != controlPlaneVersion.Major {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionControlPlaneIncompatible", fmt.Sprintf("kubernetes version major mismatch: node %d vs control-plane %d", version.Major, controlPlaneVersion.Major))
		return false
	}

	// node minor must not be greater than control-plane minor
	if version.Minor > controlPlaneVersion.Minor {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionControlPlaneIncompatible", fmt.Sprintf("kubernetes version minor too new: node %d vs control-plane %d", version.Minor, controlPlaneVersion.Minor))
		return false
	}

	// node minor must be at most three minors behind control-plane minor
	if controlPlaneVersion.Minor-version.Minor > 3 {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionControlPlaneIncompatible", fmt.Sprintf("kubernetes version minor too old: node %d vs control-plane %d", version.Minor, controlPlaneVersion.Minor))
		return false
	}

	// when both are on the same minor, node patch must not be greater than control-plane patch
	if version.Minor == controlPlaneVersion.Minor && version.Patch > controlPlaneVersion.Patch {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionControlPlaneIncompatible", fmt.Sprintf("kubernetes version patch too new: node %d vs control-plane %d", version.Patch, controlPlaneVersion.Patch))
		return false
	}

	return true
}

func requestedVersions(nodeClass *v1beta1.AKSNodeClass) (string, string) {
	if nodeClass == nil || nodeClass.Spec.Versions == nil {
		return "", ""
	}

	reqImgVer := lo.FromPtr(nodeClass.Spec.Versions.NodeImageVersion)
	reqK8sVer := lo.FromPtr(nodeClass.Spec.Versions.KubernetesVersion)

	return reqImgVer, reqK8sVer
}
