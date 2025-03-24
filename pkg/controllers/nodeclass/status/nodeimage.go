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
			RateLimiter: reasonable.RateLimiter(),
			// TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
			// Comments thread discussing this: https://github.com/Azure/karpenter-provider-azure/pull/729#discussion_r2006629809
			MaxConcurrentReconciles: 10,
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
		return reconcile.Result{}, fmt.Errorf("getting nodeimages, %w", err)
	}
	goalImages := lo.Map(nodeImages, func(nodeImage imagefamily.NodeImage, _ int) v1alpha2.NodeImage {
		reqs := lo.Map(nodeImage.Requirements.NodeSelectorRequirements(), func(item v1.NodeSelectorRequirementWithMinValues, _ int) corev1.NodeSelectorRequirement {
			return item.NodeSelectorRequirement
		})

		// sorted for consistency
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
	//
	// Note: We want to handle cases 1-3 regardless of maintenance window state, since they are either
	// for initialization, based off an underlying customer operation, or a different update we're
	// dependant upon which would have already been preformed within its required maintenance Window.
	shouldUpdate := imageVersionsUnready(nodeClass) || isMaintenanceWindowOpen()
	if !shouldUpdate {
		// Scenario B: Calculate any partial update based on image selectors, or newly supports SKUs
		goalImages = overrideAnyGoalStateVersionsWithExisting(nodeClass, goalImages)
	}

	if len(goalImages) == 0 {
		nodeClass.Status.NodeImages = nil
		nodeClass.StatusConditions().SetFalse(v1alpha2.ConditionTypeNodeImagesReady, "NodeImagesNotFound", "NodeImageSelectors did not match any NodeImages")
		logger.Info("no node images")
		return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	nodeClass.Status.NodeImages = goalImages
	nodeClass.StatusConditions().SetTrue(v1alpha2.ConditionTypeNodeImagesReady)

	logger.Debug("success")
	return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
}

// Handles case 1: This is a new AKSNodeClass, where node images haven't been populated yet
// Handles case 2: This is indirectly handling k8s version image bump, since k8s version sets this status to false
// Handles case 3: Note: like k8s we would also indirectly handle node features that required an image version bump, but none required atm.
func imageVersionsUnready(nodeClass *v1alpha2.AKSNodeClass) bool {
	return !nodeClass.StatusConditions().Get(v1alpha2.ConditionTypeNodeImagesReady).IsTrue()
}

// Handles case 4: check if the maintenance window is open
func isMaintenanceWindowOpen() bool {
	// TODO: need to add the actual logic for handling maintenance windows once it is in the ConfigMap.
	return true
}

// overrideAnyGoalStateVersionsWithExisting: will look over all the discovered images, and choose to either keep the existing version if already found in the status
// or merge the new version in. This will discard any images that are no longer selected for as well. Results in picking up new images, while also not bumping
// image versions outside of a maintenance window for existing ones.
//
// Handles case 5: users updating image selectors.
//   - Currently, this is just image family, and/or usage of SIG, which means that we should just be looking at the baseID of the images
//
// Handles case 6: We will softly add newly supported SKUs by Karpenter on their latest version
//   - Note: I think this should be re-assessed if this is the exact behavior we want to give users before any actual new SKU support is released.
//
// TODO: Need longer term design for handling newly supported versions, and other image selectors.
func overrideAnyGoalStateVersionsWithExisting(nodeClass *v1alpha2.AKSNodeClass, discoveredImages []v1alpha2.NodeImage) []v1alpha2.NodeImage {
	existingBaseIDMapping := mapImageBasesToImages(nodeClass.Status.NodeImages)
	discoveredBaseIDMapping := mapImageBasesToImages(discoveredImages)

	updatedImages := []v1alpha2.NodeImage{}
	for discoveredBaseImageID, discoveredImage := range discoveredBaseIDMapping {
		if existingImage, ok := existingBaseIDMapping[discoveredBaseImageID]; ok {
			updatedImages = append(updatedImages, *existingImage)
		} else {
			updatedImages = append(updatedImages, *discoveredImage)
		}
	}
	return updatedImages
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
// Examples:
//
// - CIG:
//   - Input: /CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd/versions/2022.10.03
//   - Output: /CommunityGalleries/AKSUbuntu-38d80f77-467a-481f-a8d4-09b6d4220bd2/images/2204gen2containerd
//
// - SIG:
//   - Input: /subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd/versions/2022.10.03
//   - Output: /subscriptions/10945678-1234-1234-1234-123456789012/resourceGroups/AKS-Ubuntu/providers/Microsoft.Compute/galleries/AKSUbuntu/images/2204gen2containerd
func trimVersionSuffix(imageID string) string {
	imageIDParts := strings.Split(imageID, "/")
	baseID := strings.Join(imageIDParts[0:len(imageIDParts)-2], "/")
	return baseID
}
