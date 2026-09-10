package status

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/kubernetesversion"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

const (
	pinningReconcilerName = "nodeclass.pinning"
)

var (
	errKubernetesVersionInvalidFormat            = errors.New("kubernetes version invalid format")
	errKubernetesVersionControlPlaneIncompatible = errors.New("kubernetes version control plane incompatible")
	errNodeImageVersionInvalid                   = errors.New("node image version invalid")
	errRollbackTargetKubernetesVersionMismatch   = errors.New("rollback target kubernetes version mismatch")
)

// PinningReconciler handles the reconciliation of pinned node images for AKSNodeClass resources.
// It ensures that the node images are correctly pinned based on the specified requirements and the current state of the cluster.
type PinningReconciler struct {
	kubernetesVersionProvider kubernetesversion.KubernetesVersionProvider
	nodeImageProvider         imagefamily.NodeImageProvider
	cm                        *pretty.ChangeMonitor
}

// NewPinningReconciler creates a new instance of the PinningReconciler.
func NewPinningReconciler(k8sProvider kubernetesversion.KubernetesVersionProvider, imgProvider imagefamily.NodeImageProvider) *PinningReconciler {
	return &PinningReconciler{
		kubernetesVersionProvider: k8sProvider,
		nodeImageProvider:         imgProvider,
		cm:                        pretty.NewChangeMonitor(),
	}
}

// Register registers the PinningReconciler with the given manager.
func (r *PinningReconciler) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(pinningReconcilerName).
		For(&v1beta1.AKSNodeClass{}).
		WithOptions(controller.Options{
			RateLimiter: reasonable.RateLimiter(),
			// TODO: Document why this magic number used. If we want to consistently use it accoss reconcilers, refactor to a reused const.
			// Comments thread discussing this: https://github.com/Azure/karpenter-provider-azure/pull/729#discussion_r2006629809
			MaxConcurrentReconciles: 10,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), r))
}

// Reconcile handles the reconciliation of pinned node images for the given AKSNodeClass.
func (r *PinningReconciler) Reconcile(ctx context.Context, nodeClass *v1beta1.AKSNodeClass) (reconcile.Result, error) {
	// Update control plane kubernetes version.
	controlPlaneVersion, err := r.kubernetesVersionProvider.KubeServerVersion(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting kubernetes version, %w", err)
	}

	// Update latest suffix
	latestImages, err := listImages(ctx, r.nodeImageProvider, *nodeClass, controlPlaneVersion)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing latest images, %w", err)
	}
	if len(latestImages) == 0 {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "RequestedNodeImageVersionUnavailable", "no latest images found for requested version")
		return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
	}

	reqImgVer, reqK8sVer, err := requestedVersions(nodeClass, controlPlaneVersion)
	if err != nil {
		switch {
		case errors.Is(err, errKubernetesVersionInvalidFormat):
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeValidationSucceeded, "KubernetesVersionInvalidFormat", err.Error())
		case errors.Is(err, errKubernetesVersionControlPlaneIncompatible):
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "KubernetesVersionControlPlaneIncompatible", err.Error())
		case errors.Is(err, errNodeImageVersionInvalid):
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeValidationSucceeded, "NodeImageVersionInvalid", err.Error())
		case errors.Is(err, errRollbackTargetKubernetesVersionMismatch):
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeValidationSucceeded, "RollbackTargetKubernetesVersionMismatch", err.Error())
		}
		return reconcile.Result{}, err
	}

	// Get the images associated with the requested version.
	nodeImages := latestImages
	if reqK8sVer != controlPlaneVersion {
		nodeImages, err = listImages(ctx, r.nodeImageProvider, *nodeClass, reqK8sVer)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("listing images, %w", err)
		}
	}

	if len(nodeImages) == 0 {
		nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "RequestedNodeImageVersionUnavailable", "no images found for requested version")
		return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
	}

	goalImages := lo.Map(nodeImages, func(nodeImage imagefamily.NodeImage, _ int) v1beta1.NodeImage {
		reqs := lo.Map(nodeImage.Requirements.NodeSelectorRequirements(), func(item v1.NodeSelectorRequirementWithMinValues, _ int) corev1.NodeSelectorRequirement {
			return corev1.NodeSelectorRequirement{Key: item.Key, Operator: item.Operator, Values: item.Values}
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

	if reqImgVer != "" {
		goalImages, err = replaceSuffixes(goalImages, reqImgVer)
		if err != nil {
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "RequestedNodeImageVersionUnavailable", fmt.Sprintf("failed to update image suffixes: %v", err))
			return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
		}
	}

	nodeClass.Status.Images = goalImages
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeImagesReady)

	nodeClass.Status.KubernetesVersion = lo.ToPtr(reqK8sVer)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeKubernetesVersionReady)
	nodeClass.StatusConditions().SetTrue(v1beta1.ConditionTypeValidationSucceeded)

	nodeClass.Status.Versions.LatestImageVersion = parseVersion(latestImages[0].ID)
	nodeClass.Status.Versions.ControlPlaneKubernetesVersion = &controlPlaneVersion

	return reconcile.Result{RequeueAfter: azurecache.KubernetesVersionTTL}, nil
}

// RequestedVersions returns the requested node image version and Kubernetes version for the given AKSNodeClass.
// It validates the requested versions and returns an error if not.
func requestedVersions(nodeClass *v1beta1.AKSNodeClass, controlPlaneVersion string) (string, string, error) {
	if nodeClass == nil || nodeClass.Spec.Versions == nil {
		return "", "", fmt.Errorf("versions are not configured")
	}

	reqImgVer := lo.FromPtr(nodeClass.Spec.Versions.NodeImageVersion)
	reqK8sVer := lo.FromPtr(nodeClass.Spec.Versions.KubernetesVersion)

	if err := validateK8sVersion(reqK8sVer, controlPlaneVersion); err != nil {
		return "", "", fmt.Errorf("%w: %v", errKubernetesVersionControlPlaneIncompatible, err)
	}

	if reqImgVer == "" {
		return reqImgVer, reqK8sVer, nil
	}

	if err := validatePinning(reqImgVer, reqK8sVer, nodeClass, controlPlaneVersion); err != nil {
		return "", "", fmt.Errorf("validating pinning, %w", err)
	}
	return reqImgVer, reqK8sVer, nil
}

func validRollback(reqK8sVersion, reqImageVersion string, nodeClass *v1beta1.AKSNodeClass) bool {
	if nodeClass == nil || nodeClass.Status.Versions == nil || nodeClass.Status.Versions.RecentlyUsedVersions == nil {
		return false
	}

	for _, used := range nodeClass.Status.Versions.RecentlyUsedVersions {
		if lo.FromPtr(used.KubernetesVersion) == reqK8sVersion && lo.FromPtr(used.ImageVersion) == reqImageVersion {
			return true
		}
	}

	return false
}

func validatePinning(reqImgVer, reqK8sVer string, nodeClass *v1beta1.AKSNodeClass, controlPlaneVersion string) error {
	if nodeClass == nil || nodeClass.Status.Versions == nil || nodeClass.Status.Versions.ControlPlaneKubernetesVersion == nil {
		return fmt.Errorf("control plane kubernetes version is not available")
	}

	if _, err := semver.Parse(reqK8sVer); err != nil {
		return fmt.Errorf("%w: parsing kubernetes version: %v", errKubernetesVersionInvalidFormat, err)
	}

	latestImgVer := nodeClass.Status.Versions.LatestImageVersion

	var currentImgVer string
	if len(nodeClass.Status.Images) > 0 {
		currentImgVer = parseVersion(nodeClass.Status.Images[0].ID)
	}

	switch {
	case reqImgVer == currentImgVer:
		if curVer := nodeClass.Status.KubernetesVersion; curVer != nil && reqK8sVer != *curVer {
			return fmt.Errorf("%w: requested image version is current but does not match the current k8s version", errNodeImageVersionInvalid)
		}
	case reqImgVer == latestImgVer:
		if ctrlPlaneVer := nodeClass.Status.Versions.ControlPlaneKubernetesVersion; ctrlPlaneVer != nil && reqK8sVer != *ctrlPlaneVer {
			return fmt.Errorf("%w: requested image version is latest but does not match the control plane version", errNodeImageVersionInvalid)
		}
	case !validRollback(reqK8sVer, reqImgVer, nodeClass):
		return fmt.Errorf("%w: unable to find a valid rollback for requested image version: %s", errRollbackTargetKubernetesVersionMismatch, reqImgVer)
	}
	return nil
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
func validateK8sVersion(version, controlPlaneVersion string) error {
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

	// TODO: Check AKS version metadata

	return nil
}

func replaceSuffixes(images []v1beta1.NodeImage, newSuffix string) ([]v1beta1.NodeImage, error) {
	for i, image := range images {
		if ver := parseVersion(image.ID); ver != newSuffix {
			image.ID = strings.Replace(image.ID, ver, newSuffix, 1)
			images[i] = image
		}

		if parseVersion(image.ID) != newSuffix {
			return nil, fmt.Errorf("failed to replace image version suffix for image ID: %s", image.ID)
		}
	}
	return images, nil
}
