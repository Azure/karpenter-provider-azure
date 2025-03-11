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
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/samber/lo"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

type NodeImageReconciler struct {
	nodeImageProvider imagefamily.NodeImageProvider
	cm                *pretty.ChangeMonitor
}

func NewNodeImageReconciler(provider imagefamily.NodeImageProvider) *NodeImageReconciler {
	return &NodeImageReconciler{
		nodeImageProvider: provider,
		cm:                pretty.NewChangeMonitor(),
	}
}

func (r *NodeImageReconciler) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodeclass.nodeimage").
		For(&v1alpha2.AKSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), r))
}

// The image version reconciler will detect reasons to bump the node image version as follows in order:
// 1. ~~Update any missing NodeImages~~ Updated: Initializes the images we should use based on customer configuration.
// 2. Indirectly handle image bump for k8s upgrade
// 3. Can indirectly handle bumps for any Images unsupported by Node Features, if required to in the future
// 4. TODO: Update NodeImages to latest if in a MW (retrieved from ConfigMap)
// 5 NEW: Handles update cases when customer changes image family, SIG usage, or other means of image selectors
func (r *NodeImageReconciler) Reconcile(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (reconcile.Result, error) {
	logger := logging.FromContext(ctx)
	logger.Debug("nodeclass.nodeimage: starting reconcile")

	nodeImages, err := r.nodeImageProvider.List(ctx, nodeClass)
	if err != nil {
		logger.Debug("nodeclass.nodeimage: err listing node images")
		return reconcile.Result{}, fmt.Errorf("getting nodeimages, %w", err)
	}
	images := lo.Map(nodeImages, func(nodeImage imagefamily.NodeImage, _ int) v1alpha2.Image {
		reqs := lo.Map(nodeImage.Requirements.NodeSelectorRequirements(), func(item v1.NodeSelectorRequirementWithMinValues, _ int) corev1.NodeSelectorRequirement {
			return item.NodeSelectorRequirement
		})

		sort.Slice(reqs, func(i, j int) bool {
			if len(reqs[i].Key) != len(reqs[j].Key) {
				return len(reqs[i].Key) < len(reqs[j].Key)
			}
			return reqs[i].Key < reqs[j].Key
		})
		return v1alpha2.Image{
			ID:           nodeImage.ID,
			Requirements: reqs,
		}
	})
	imageBases := map[string]bool{}
	for _, nodeImage := range nodeImages {
		imageBases[nodeImage.BaseID] = true
	}

	// Case 1: node images haven't been populated for the nodeclass yet, or out of date (k8s upgrade, etc)
	// Case 2: This is indirectly handling k8s version image bump, since k8s version sets this status to false
	// Case 3: Note: like k8s we would also indirectly handle Node Sig features that required an image version bump, but none required atm.
	shouldUpdate := !nodeClass.StatusConditions().Get(v1alpha2.ConditionTypeNodeImageReady).IsTrue()
	logger.Debugf("nodeclass.nodeimage: should init/k8s upgrade: %t", shouldUpdate)

	var removedImages map[string]bool
	if !shouldUpdate {
		_, removedImages = nodeImageDelta(nodeClass, imageBases)

		// Case 5: Handles the current case of users updating image family, and/or usage of SIG.
		// TODO: should we automatically soft add newly supported skus, and what about only partial removal due to selectors.
		if len(removedImages) != 0 {
			shouldUpdate = true
		} else {
			shouldUpdate = isOpenMW()
		}
	}
	logger.Debugf("nodeclass.nodeimage: should update overall: %t", shouldUpdate)

	if shouldUpdate {
		if len(images) == 0 {
			nodeClass.Status.Images = nil
			nodeClass.StatusConditions().SetFalse(v1alpha2.ConditionTypeNodeImageReady, "NodeImagesNotFound", "NodeImageSelectors did not match any NodeImages")
			logger.Info("nodeclass.nodeimage: no images")
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}

		nodeClass.Status.Images = images
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeNodeImageReady)
	} // else if len(newImages) != 0 { // Preform partial update? }

	logger.Debug("nodeclass.nodeimage: success")
	return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
}

func nodeImageDelta(nodeClass *v1alpha2.AKSNodeClass, imageBases map[string]bool) (map[string]bool, map[string]bool) {
	currentNodeImagesBaseIDs := map[string]bool{}
	for _, nodeImage := range nodeClass.Status.Images {
		imageIDParts := strings.Split(nodeImage.ID, "/")
		baseID := strings.Join(imageIDParts[0:len(imageIDParts)-2], "/")
		currentNodeImagesBaseIDs[baseID] = true
	}

	newImages := map[string]bool{}
	for imageBase, _ := range imageBases {
		if _, found := currentNodeImagesBaseIDs[imageBase]; !found {
			newImages[imageBase] = true
		}
	}

	removedImages := map[string]bool{}
	for currentNodeImage, _ := range currentNodeImagesBaseIDs {
		if _, found := imageBases[currentNodeImage]; !found {
			removedImages[currentNodeImage] = true
		}
	}
	return newImages, removedImages
}

func isOpenMW() bool {
	// Case 4: check if MW is open
	// TODO: once MW window is in ConfigMap.
	return true //TODO
}
