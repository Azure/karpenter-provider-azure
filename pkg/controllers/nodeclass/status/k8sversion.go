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
	"github.com/blang/semver/v4"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The upgrade controller will detect reasons to bump the node image version as follows in order:
// 1. ~~Update any missing NodeImages~~ Updated: Initializes the images we should use based on customer configuration.
// 2. Handle K8s Upgrade + Image Bump
// 3. Handle bumps for any Images unsupported by Node Features
// 4. Update NodeImages to latest if in a MW (retrieved from ConfigMap)
func (ni *NodeImage) ReconcileK8s(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (reconcile.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Debug("nodeclass.k8sversion: starting reconcile")

	k8sVersion, err := ni.nodeImageProvider.KubeServerVersion(ctx)
	if err != nil {
		logger.Debug("nodeclass.k8sversion: err getting k8s version")
		return reconcile.Result{}, fmt.Errorf("getting k8s version, %w", err)
	}

	// Case 1: init, update k8s status to version found
	if !nodeClass.StatusConditions().Get(v1alpha2.ConditionTypeK8sVersionReady).IsTrue() || nodeClass.Status.K8sVersion == "" {
		logger.Debug("nodeclass.k8sversion: init k8s version")
		nodeClass.Status.K8sVersion = k8sVersion
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeK8sVersionReady)
	} else {
		// Check if there is an upgrade
		newK8sVersion, err := semver.Parse(k8sVersion)
		if err != nil {
			logger.Debug("nodeclass.k8sversion: err parsing new k8s version")
			return reconcile.Result{}, fmt.Errorf("parsing discovered k8s version, %w", err)
		}
		currentK8sVersion, err := semver.Parse(nodeClass.Status.K8sVersion)
		if err != nil {
			logger.Debug("nodeclass.k8sversion: err parsing current k8s version")
			return reconcile.Result{}, fmt.Errorf("parsing current k8s version, %w", err)
		}
		// Case 2: Upgrade k8s version [Note: we set node image to not ready, since we upgrade node image when there is a k8s upgrade]
		if newK8sVersion.GT(currentK8sVersion) {
			logger.Debug("nodeclass.k8sversion: k8s upgrade detected")
			nodeClass.Status.K8sVersion = k8sVersion
			nodeClass.StatusConditions().SetFalse(v1alpha2.ConditionTypeNodeImageReady, "K8sUpgrade", "Preforming K8s upgrade, need to get latest node images")
			nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeK8sVersionReady)
		}
	}
	logger.Debug("nodeclass.k8sversion: successful reconcile")
	return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
}
