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
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/logging"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

const (
	nodeImageReconcilerName = "nodeclass.images"

	// ConfigMap consts
	maintenanceWindowConfigMapName = "upcoming-maintenance-window"
	nodeOSMaintenanceWindowChannel = "aksManagedNodeOSUpgradeSchedule"
	configMapStartTimeFormat       = "%s-start"
	configMapEndTimeFormat         = "%s-end"
)

type NodeImageReconciler struct {
	nodeImageProvider            imagefamily.NodeImageProvider
	inClusterKubernetesInterface kubernetes.Interface
	systemNamespace              string
	cm                           *pretty.ChangeMonitor
}

func NewNodeImageReconciler(
	provider imagefamily.NodeImageProvider,
	inClusterKubernetesInterface kubernetes.Interface,
) *NodeImageReconciler {
	systemNamespace := strings.TrimSpace(os.Getenv("SYSTEM_NAMESPACE"))

	return &NodeImageReconciler{
		nodeImageProvider:            provider,
		inClusterKubernetesInterface: inClusterKubernetesInterface,
		systemNamespace:              systemNamespace,
		cm:                           pretty.NewChangeMonitor(),
	}
}

func (r *NodeImageReconciler) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(nodeImageReconcilerName).
		For(&v1beta1.AKSNodeClass{}).
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
//   - 4. TODO: Update Images to latest if in an open maintenance window [retrieved from ConfigMap]
//
// Scenario B: Calculate images to be updated based on delta of available images
//   - 5. Handles update cases when customer changes image family, SIG usage, or other means of image selectors
//   - 6. Handles softly adding newest image version of any newly supported SKUs by Karpenter
//
// Scenario C: Upgrade expired images
//   - 7. Upgrades Linux images that are more than 90 days old to comply with AKS requirements
//
// Note: While we'd currently only need to store a SKU -> version mapping in the status for avilaible Images
// we decided to store the full image ID, plus Requirements associated with it. Storing the complete ID is a simple
// and clean approach while allowing us to extend future capabilities off of it. Additionally, while the decision to
// store Requirements adds minor bloat, it also provides extra visibility into the avilaible images and how their
// selection will work, which is seen as worth the tradeoff.
func (r *NodeImageReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName(nodeImageReconcilerName))
	logger := log.FromContext(ctx)

	// validate FIPS + useSIG
	fipsMode := nodeClass.Spec.FIPSMode
	useSIG := options.FromContext(ctx).UseSIG
	if lo.FromPtr(fipsMode) == v1beta1.FIPSModeFIPS && !useSIG {
		nodeClass.Status.Images = nil
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "SIGRequiredForFIPS", "FIPS images require UseSIG to be enabled, but UseSIG is false (note: UseSIG is only supported in AKS managed NAP)")
		logger.Info("FIPS images require SIG", "error", fmt.Errorf("FIPS images require UseSIG to be enabled, but UseSIG is false (note: UseSIG is only supported in AKS managed NAP)"))
		return reconcile.Result{}, nil
	}

	nodeImages, err := r.nodeImageProvider.List(ctx, nodeClass)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting nodeimages, %w", err)
	}
	goalImages := lo.Map(nodeImages, func(nodeImage imagefamily.NodeImage, _ int) v1beta1.NodeImage {
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
		return v1beta1.NodeImage{
			ID:           nodeImage.ID,
			Requirements: reqs,
		}
	})

	// Scenario A: Check if we should do a full update to latest before processing any partial update
	//
	// Note: We want to handle cases 1-3 regardless of maintenance window state, since they are either
	// for initialization, based off an underlying customer operation, or a different update we're
	// dependant upon which would have already been preformed within its required maintenance Window.
	shouldUpdate := imageVersionsUnready(nodeClass)
	if !shouldUpdate {
		// Case 4: Check if the maintenance window is open
		shouldUpdate, err = r.isMaintenanceWindowOpen(ctx)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("checking maintenance window, %w", err)
		}
	}
	if !shouldUpdate {
		// Scenario B: Calculate any partial update based on image selectors, or newly supports SKUs
		updatedImages := overrideAnyGoalStateVersionsWithExisting(nodeClass, goalImages)
		// Scenario C: Upgrade any images that are more than 90 days old (AKS compliance requirement)
		goalImages = upgradeAnyExpiredImages(ctx, updatedImages, goalImages)
	}

	if len(goalImages) == 0 {
		nodeClass.Status.Images = nil
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNotFound", "ImageSelectors did not match any Images")
		logger.Info("no available node images")
		return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// We care about the ordering of the slices here, as it translates to priority during selection, so not treating them as sets
	if utils.HasChanged(nodeClass.Status.Images, goalImages, &hashstructure.HashOptions{SlicesAsSets: false}) {
		logger.Info("new available images updated for nodeclass", "existingImages", nodeClass.Status.Images, "newImages", goalImages)
	}
	nodeClass.Status.Images = goalImages
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)
	return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
}

// Handles case 1: This is a new AKSNodeClass, where images haven't been populated yet
// Handles case 2: This is indirectly handling k8s version image bump, since k8s version sets this status to false
// Handles case 3: Note: like k8s we would also indirectly handle node features that required an image version bump, but none required atm.
func imageVersionsUnready(nodeClass *v1beta1.AKSNodeClass) bool {
	return !nodeClass.StatusConditions().Get(v1beta1.ConditionTypeImagesReady).IsTrue()
}

// Handles case 4: check if the maintenance window is open
// TODO (charliedmcb): remove nolint on gocyclo. Added for now in order to pass "make verify"
// I think the best way to get rid of gocyclo is to break the section retrieving the maintenance window
// range from the ConfigMap into its own helper function using channel as a parameter.
// nolint: gocyclo
func (r *NodeImageReconciler) isMaintenanceWindowOpen(ctx context.Context) (bool, error) {
	logger := log.FromContext(ctx)
	if r.systemNamespace == "" {
		// We fail open here, since the default case should be to upgrade
		return true, nil
	}

	mwConfigMap, err := r.inClusterKubernetesInterface.CoreV1().ConfigMaps(r.systemNamespace).Get(ctx, maintenanceWindowConfigMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// We fail open here, since the default case should be to upgrade
			return true, nil
		}
		return false, fmt.Errorf("error getting maintenance window configmap, %w", err)
	}
	// Monitoring the entire ConfigMap's data might catch more data changes than we care about. However, I think it makes sense to monitor
	//     here as it does catch the entire spread of cases we care about, and will give us direct insight on the raw data.
	// Note: we don't need to add the nodeclass name into the monitoring here, as we actually want the entries to collide, since
	//     maintenance windows are a cluster level concept, rather that a nodeclass level type, meaning we'd have repeat redundant info
	//     if scoping to the nodeclass.
	// TODO: In the longer run, the maintenance window handling should be factored out into a sharable provider, rather than being contained
	//     within the image controller itself.
	if r.cm.HasChanged("nodeclass-maintenancewindowdata", mwConfigMap.Data) {
		logger.Info("new maintenance window data discovered", "maintenanceWindowData", mwConfigMap.Data)
	}
	if len(mwConfigMap.Data) == 0 {
		// An empty configmap means there's no maintenance windows defined, and its up to us when to preform maintenance
		return true, nil
	}

	nextNodeOSMWStartStr, okStart := mwConfigMap.Data[fmt.Sprintf(configMapStartTimeFormat, nodeOSMaintenanceWindowChannel)]
	nextNodeOSMWEndStr, okEnd := mwConfigMap.Data[fmt.Sprintf(configMapEndTimeFormat, nodeOSMaintenanceWindowChannel)]
	if !okStart && !okEnd {
		// No maintenance window defined for aksManagedNodeOSUpgradeSchedule, so its up to us when to preform maintenance
		return true, nil
	} else if (okStart && !okEnd) || (!okStart && okEnd) {
		return false, fmt.Errorf("unexpected state, with incomplete maintenance window data for channel %s", nodeOSMaintenanceWindowChannel)
	}

	nextNodeOSMWStart, err := time.Parse(time.RFC3339, nextNodeOSMWStartStr)
	if err != nil {
		return false, fmt.Errorf("error parsing maintenance window start time for channel %s, %w", nodeOSMaintenanceWindowChannel, err)
	}
	nextNodeOSMWEnd, err := time.Parse(time.RFC3339, nextNodeOSMWEndStr)
	if err != nil {
		return false, fmt.Errorf("error parsing maintenance window end time for channel %s, %w", nodeOSMaintenanceWindowChannel, err)
	}

	now := time.Now().UTC()

	return now.After(nextNodeOSMWStart.UTC()) && now.Before(nextNodeOSMWEnd.UTC()), nil
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
func overrideAnyGoalStateVersionsWithExisting(nodeClass *v1beta1.AKSNodeClass, discoveredImages []v1beta1.NodeImage) []v1beta1.NodeImage {
	existingBaseIDMapping := mapImageBasesToImages(nodeClass.Status.Images)

	updatedImages := []v1beta1.NodeImage{}
	// Note: we have to range over the discovered images here, instead of converting to a baseIDMapping, to keep the ordering consistent
	for i := range discoveredImages {
		discoveredImage := discoveredImages[i]
		discoveredBaseImageID := trimVersionSuffix(discoveredImage.ID)
		if existingImage, ok := existingBaseIDMapping[discoveredBaseImageID]; ok {
			updatedImages = append(updatedImages, *existingImage)
		} else {
			updatedImages = append(updatedImages, discoveredImage)
		}
	}
	return updatedImages
}

func mapImageBasesToImages(images []v1beta1.NodeImage) map[string]*v1beta1.NodeImage {
	imagesBaseMapping := map[string]*v1beta1.NodeImage{}
	for i := range images {
		baseID := trimVersionSuffix(images[i].ID)
		imagesBaseMapping[baseID] = &images[i]
	}
	return imagesBaseMapping
}

// upgradeAnyExpiredImages upgrades any Linux images that are more than 90 days old to comply with AKS requirements.
// For Linux images, it parses the version format YYYYMM.DD.PATCH and checks if the image is older than 90 days.
// TODO: Add support for Windows image version format (OS.PATCH.YYMMDD) when needed.
func upgradeAnyExpiredImages(ctx context.Context, futureImages []v1beta1.NodeImage, foundImages []v1beta1.NodeImage) []v1beta1.NodeImage {
	goalBaseIDMapping := mapImageBasesToImages(foundImages)
	finalImages := []v1beta1.NodeImage{}
	now := time.Now().UTC()

	for _, futureImage := range futureImages {
		baseID := trimVersionSuffix(futureImage.ID)
		foundImage, exists := goalBaseIDMapping[baseID]
		if !exists {
			// > Note: this shouldn't be able to happen since overrideAnyGoalStateVersionsWithExisting
			// > ensures all found base image ids end up in goal/future images.
			// No corresponding goal image, keep current
			finalImages = append(finalImages, futureImage)
			continue
		}

		// Check if current image is expired (Linux only for now)
		expired, err := isImageExpired(futureImage.ID, now)
		if err != nil {
			// Log malformed image and replace it
			log.FromContext(ctx).Info("malformed image version, replacing", logging.ImageID, futureImage.ID, "error", err)
			finalImages = append(finalImages, *foundImage)
			continue
		}

		if expired {
			// Use the newer goal image version
			finalImages = append(finalImages, *foundImage)
		} else {
			// Keep the current image
			finalImages = append(finalImages, futureImage)
		}
	}

	return finalImages
}

// isLinuxImageVersion checks if a version string follows the Linux image format YYYYMM.DD.PATCH.
func isLinuxImageVersion(version string) bool {
	// Regex for YYYYMM.DD.PATCH format
	linuxVersionRegex := regexp.MustCompile(`^\d{6}\.\d{2}\.\d+$`)
	return linuxVersionRegex.MatchString(version)
}

// parseLinuxImage parses a Linux image version in YYYYMM.DD.PATCH format.
// Returns year, month, day, patch, and error if parsing fails.
func parseLinuxImage(version string) (string, string, string, string, error) {
	versionParts := strings.Split(version, ".")
	if len(versionParts) < 3 {
		return "", "", "", "", fmt.Errorf("invalid version format: expected YYYYMM.DD.PATCH, got %s", version)
	}

	// Extract YYYYMM.DD.PATCH parts
	if len(versionParts[0]) != 6 || len(versionParts[1]) != 2 {
		return "", "", "", "", fmt.Errorf("invalid version format: expected YYYYMM.DD.PATCH, got %s", version)
	}

	year := versionParts[0][:4]
	month := versionParts[0][4:6]
	day := versionParts[1]
	patch := versionParts[2]

	return year, month, day, patch, nil
}

// isImageExpired checks if a image version is more than 90 days old.
// > NOTE: this only supports Linux image versions follow the format YYYYMM.DD.PATCH (e.g., 202410.03.1).
// > An error will be returned if the image isn't using the Linux version format.
// TODO: Add support for Windows image version format (OS.PATCH.YYMMDD) when needed
func isImageExpired(imageID string, now time.Time) (bool, error) {
	version := extractVersionFromImageID(imageID)
	if version == "" {
		return false, fmt.Errorf("could not extract version from image ID: %s", imageID)
	}

	// Check if this is a Linux image version
	if !isLinuxImageVersion(version) {
		// Not a Linux image, return false (not expired) without error
		return false, nil
	}

	year, month, day, _, err := parseLinuxImage(version)
	if err != nil {
		// TODO: Add support for Windows image version format (OS.PATCH.YYMMDD) when needed
		return false, fmt.Errorf("parsing Linux image version: %w", err)
	}

	// Parse the date
	dateStr := fmt.Sprintf("%s-%s-%s", year, month, day)
	imageDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return false, fmt.Errorf("parsing image date %s: %w", dateStr, err)
	}

	// Check if more than 90 days old (expire 30 minutes early to provide buffer)
	// This ensures we don't accidentally try to provision with an expired image
	return now.Sub(imageDate) > (90*24*time.Hour - 30*time.Minute), nil
}

// extractVersionFromImageID extracts the version part from an image ID.
func extractVersionFromImageID(imageID string) string {
	parts := strings.Split(imageID, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
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
