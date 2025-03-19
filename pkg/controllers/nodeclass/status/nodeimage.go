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

const (
	nodeImageReconcilerName = "nodeclass.nodeimage"
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
		Named(nodeImageReconcilerName).
		For(&v1alpha2.AKSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter:             reasonable.RateLimiter(),
			MaxConcurrentReconciles: 10, // TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), r))
}

// The image version reconciler will detect reasons to bump the node image version as follows in order:
//
// Scenario A: Update all image versions to latest
//   - 1. Initializes the images versions for a newly created AKSNodeClass, based on customer configuration.
//   - 2. Indirectly handle image bump for k8s upgrade
//   - 3. Can indirectly handle bumps for any images unsupported by node features, if required to in the future
//     Note: Currently there are no node features to be handled in this way.
//   - 4. TODO: Update NodeImages to latest if in an open maintenance window [retrieved from ConfigMap]
//
// Scenario B: Calculate images to be updated based on delta of available images
//   - 5. Handles update cases when customer changes image family, SIG usage, or other means of image selectors
//   - 6. Handles softly adding newest image version of any newly supported SKUs by Karpenter
func (r *NodeImageReconciler) Reconcile(ctx context.Context, nodeClass *v1alpha2.AKSNodeClass) (reconcile.Result, error) {
	ctx = logging.WithLogger(ctx, logging.FromContext(ctx).Named(nodeImageReconcilerName))
	logger := logging.FromContext(ctx)
	logger.Debug("starting reconcile")

	nodeImages, err := r.nodeImageProvider.List(ctx, nodeClass)
	if err != nil {
		logger.Debug("err listing node images")
		return reconcile.Result{}, fmt.Errorf("getting nodeimages, %w", err)
	}
	images := lo.Map(nodeImages, func(nodeImage imagefamily.NodeImage, _ int) v1alpha2.NodeImage {
		reqs := lo.Map(nodeImage.Requirements.NodeSelectorRequirements(), func(item v1.NodeSelectorRequirementWithMinValues, _ int) corev1.NodeSelectorRequirement {
			return item.NodeSelectorRequirement
		})

		sort.Slice(reqs, func(i, j int) bool {
			if len(reqs[i].Key) != len(reqs[j].Key) {
				return len(reqs[i].Key) < len(reqs[j].Key)
			}
			return reqs[i].Key < reqs[j].Key
		})
		return v1alpha2.NodeImage{
			ID:           nodeImage.ID,
			Requirements: reqs,
		}
	})

	// Scenario A: Check if we should do a full update to latest before processing any partial update
	shouldUpdate := imageVersionsUnready(nodeClass) || isMaintenanceWindowOpen()
	logger.Debugf("should complete a full update to latest: %t", shouldUpdate)
	if !shouldUpdate {
		// Scenario B: Check if we should do any partial update based on image selectors, or newly supports SKUs
		images, shouldUpdate = processPartialUpdate(nodeClass, images)
	}

	logger.Debugf("should update overall: %t", shouldUpdate)
	if shouldUpdate {
		if len(images) == 0 {
			nodeClass.Status.NodeImages = nil
			nodeClass.StatusConditions().SetFalse(v1alpha2.ConditionTypeNodeImageReady, "NodeImagesNotFound", "NodeImageSelectors did not match any NodeImages")
			logger.Info("no images")
			return reconcile.Result{RequeueAfter: time.Minute}, nil
		}

		nodeClass.Status.NodeImages = images
		nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeNodeImageReady)
	}

	logger.Debug("success")
	return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
}

// Case 1: This is a new AKSNodeClass, where node images haven't been populated yet
// Case 2: This is indirectly handling k8s version image bump, since k8s version sets this status to false
// Case 3: Note: like k8s we would also indirectly handle node features that required an image version bump, but none required atm.
func imageVersionsUnready(nodeClass *v1alpha2.AKSNodeClass) bool {
	return !nodeClass.StatusConditions().Get(v1alpha2.ConditionTypeNodeImageReady).IsTrue()
}

func isMaintenanceWindowOpen() bool {
	// Case 4: check if the maintenance window is open
	// TODO: need to add the actual logic for handling maintenance windows once it is in the ConfigMap.
	return true
}

// Case 5: Handles the case of users updating image selectors.
//   - Currently, this is just image family, and/or usage of SIG, which means that we should just be looking at the baseID of the images
//
// Case 6: We will softly add newly supported SKUs by Karpenter on their latest version
//   - Note: I think this should be assess, if this is the exact behavior we want to give users, before any actual new SKU support is released.
//
// TODO: Need longer term design for handling newly supported versions, and other image selectors.
func processPartialUpdate(nodeClass *v1alpha2.AKSNodeClass, discoveredImages []v1alpha2.NodeImage) ([]v1alpha2.NodeImage, bool) {
	existingBaseIDMapping := mapImageBasesToImages(nodeClass.Status.NodeImages)
	discoveredBaseIDMapping := mapImageBasesToImages(discoveredImages)

	updatedImages := []v1alpha2.NodeImage{}
	foundUpdate := false
	for discoveredBaseImageID, discoveredImage := range discoveredBaseIDMapping {
		if existingImage, ok := existingBaseIDMapping[discoveredBaseImageID]; ok {
			updatedImages = append(updatedImages, *existingImage)
		} else {
			foundUpdate = true
			updatedImages = append(updatedImages, *discoveredImage)
		}
	}
	return updatedImages, foundUpdate
}

func mapImageBasesToImages(images []v1alpha2.NodeImage) map[string]*v1alpha2.NodeImage {
	imagesBaseMapping := map[string]*v1alpha2.NodeImage{}
	for i := range images {
		baseID := trimVersionSuffix(images[i].ID)
		imagesBaseMapping[baseID] = &images[i]
	}
	return imagesBaseMapping
}

// Trims off the version suffix, and leaves just the image base id
func trimVersionSuffix(imageID string) string {
	imageIDParts := strings.Split(imageID, "/")
	baseID := strings.Join(imageIDParts[0:len(imageIDParts)-2], "/")
	return baseID
}
