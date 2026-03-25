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

package cloudprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	corenodeclaimutils "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"
)

const (
	NodeClassDrift       cloudprovider.DriftReason = "NodeClassDrift"
	K8sVersionDrift      cloudprovider.DriftReason = "K8sVersionDrift"
	ImageDrift           cloudprovider.DriftReason = "ImageDrift"
	KubeletIdentityDrift cloudprovider.DriftReason = "KubeletIdentityDrift"
	ClusterConfigDrift   cloudprovider.DriftReason = "ClusterConfigDrift" // This is a catch-all for cluster-level config changes (e.g., from PUT ManagedCluster), where Karpenter does not directly "own" them.

	// TODO (charliedmcb): Use this const across code and test locations which are signaling/checking for "no drift"
	NoDrift cloudprovider.DriftReason = ""
)

func (c *CloudProvider) isNodeClassDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	// TODO: if we find more expensive checks, such as reading VMs or NICs from Azure, are being duplicated between checks, we should
	//       produce a lazy at-most-once that allows a check to cache a value for later checks to read.

	if nodeClaim == nil || nodeClaim.Status.ProviderID == "" {
		// This is technically not possible (as of the time of writing). IsDrifted() won't be called by core until NodeClaim is launched.
		return "", fmt.Errorf("nodeclaim %s is missing provider ID", nodeClaim.Name)
	}

	var checks []func(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error)
	if _, isAKSMachine := instance.GetAKSMachineNameFromNodeClaim(nodeClaim); isAKSMachine {
		checks = []func(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error){
			c.areStaticFieldsDrifted,
			c.isK8sVersionDrifted,
			c.isImageVersionDrifted,
			c.isMachineDrifted,
		}
	} else {
		// For legacy nodes
		checks = []func(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error){
			c.areStaticFieldsDrifted,
			c.isK8sVersionDrifted,
			c.isKubeletIdentityDrifted,
			c.isImageVersionDrifted,
		}
	}

	for _, check := range checks {
		driftReason, err := check(ctx, nodeClaim, nodeClass)
		if err != nil {
			return "", err
		}
		if driftReason != "" {
			return driftReason, nil
		}
	}

	return "", nil
}

func (c *CloudProvider) areStaticFieldsDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)

	nodeClassHash, foundNodeClassHash := nodeClass.Annotations[v1beta1.AnnotationAKSNodeClassHash]
	nodeClassHashVersion, foundNodeClassHashVersion := nodeClass.Annotations[v1beta1.AnnotationAKSNodeClassHashVersion]
	nodeClaimHash, foundNodeClaimHash := nodeClaim.Annotations[v1beta1.AnnotationAKSNodeClassHash]
	nodeClaimHashVersion, foundNodeClaimHashVersion := nodeClaim.Annotations[v1beta1.AnnotationAKSNodeClassHashVersion]

	if !foundNodeClassHash || !foundNodeClaimHash || !foundNodeClassHashVersion || !foundNodeClaimHashVersion {
		return "", nil
	}
	// validate that the hash version for the AKSNodeClass is the same as the NodeClaim before evaluating for static drift
	if nodeClassHashVersion != nodeClaimHashVersion {
		return "", nil
	}

	if nodeClassHash != nodeClaimHash {
		logger.V(1).Info("drift triggered as nodeClassHash != nodeClaimHash",
			"driftType", NodeClassDrift,
			"nodeClassHash", nodeClassHash,
			"nodeClaimHash", nodeClaimHash)
		return NodeClassDrift, nil
	}

	return "", nil
}

func (c *CloudProvider) isK8sVersionDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)

	k8sVersion, err := nodeClass.GetKubernetesVersion()
	// Note: this differs from AWS, as they don't check for status readiness during Drift.
	if err != nil {
		// Note: we don't consider this a hard failure for drift if the KubernetesVersion is invalid/not ready to use, so we ignore returning the error here.
		// We simply ensure the stored version is valid and ready to use, if we are to calculate potential Drift based on it.
		// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found across all of IsDrifted.
		logger.Info("kubernetes version not ready, skipping drift check", "error", err)
		return "", nil
	}

	node, err := c.getNodeForDrift(ctx, nodeClaim)
	if err != nil || node == nil {
		return "", err
	}

	nodeK8sVersion := strings.TrimPrefix(node.Status.NodeInfo.KubeletVersion, "v")
	if nodeK8sVersion != k8sVersion {
		logger.V(1).Info("drift triggered due to k8s version mismatch",
			"driftType", K8sVersionDrift,
			"expectedKubernetesVersion", k8sVersion,
			"actualKubernetesVersion", nodeK8sVersion)
		return K8sVersionDrift, nil
	}
	return "", nil
}

func (c *CloudProvider) isImageVersionDrifted(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	nodeClass *v1beta1.AKSNodeClass,
) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)
	nodeImages, err := nodeClass.GetImages()
	// Note: this differs from AWS, as they don't check for status readiness during Drift.
	if err != nil {
		// Note: we don't consider this a hard failure for drift if the Images are not ready to use, so we ignore returning the error here.
		// The stored Images must be ready to use if we are to calculate potential Drift based on them.
		// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found across all of IsDrifted.
		logger.Info("node image not ready, skipping drift check", "error", err)
		return "", nil
	}
	if len(nodeImages) == 0 {
		// Note: this case shouldn't happen, since if there are no nodeImages, the ConditionTypeImagesReady should be false.
		//     However, if it would happen, we want this to error, as it means the NodeClass is in a state it can't provision nodes.
		return "", fmt.Errorf("no images exist for the given constraints")
	}

	// bail out early if core called this before the node is done creating.
	if nodeClaim.Status.ImageID == "" {
		return "", fmt.Errorf("no image ID found in nodeClaim status")
	}

	if _, isAKSMachine := instance.GetAKSMachineNameFromNodeClaim(nodeClaim); isAKSMachine {
		for _, availableImage := range nodeImages {
			// Note: not supporting drift across galleries yet, as AKS machine does not hold gallery info, as of now.
			// Alternatively, could call GET VM, if not propose API changes.
			availableImageVersion, err := utils.GetAKSMachineNodeImageVersionFromImageID(availableImage.ID) // WARNING: verify whether this function support the desired gallery
			if err != nil {
				logger.Error(err, "failed to convert image ID to AKS machine node image version", "imageID", availableImage.ID)
				continue
			}
			if availableImageVersion == nodeClaim.Status.ImageID {
				return "", nil
			}
		}
	} else {
		for _, availableImage := range nodeImages {
			if availableImage.ID == nodeClaim.Status.ImageID {
				return "", nil
			}
		}
	}

	logger.V(1).Info("drift triggered as actual image version was not found in the set of currently available node images",
		"driftType", ImageDrift,
		"actualImageVersion", nodeClaim.Status.ImageID)
	return ImageDrift, nil
}

// isKubeletIdentityDrifted returns drift if the kubelet identity has drifted
func (c *CloudProvider) isKubeletIdentityDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, _ *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	opts := options.FromContext(ctx)
	logger := log.FromContext(ctx)

	node, err := c.getNodeForDrift(ctx, nodeClaim)
	if err != nil || node == nil {
		return "", err
	}

	kubeletIdentityClientID := node.Labels[v1beta1.AKSLabelKubeletIdentityClientID]
	// The kubelet identity label is supposed to be set on every node, but prior to
	// 1.4.0 it was not set by Karpenter. In order to avoid rolling all existing nodes,
	// we don't count a missing kubelet identity as drift. This situation should resolve itself as
	// image version and Kubernetes version drift is performed.
	// TODO: This short-circuit should be removed post 1.4.0 (~2025-07-01)
	if kubeletIdentityClientID == "" {
		return "", nil
	}

	if kubeletIdentityClientID != opts.KubeletIdentityClientID {
		logger.V(1).Info("drift triggered due to expected and actual kubelet identity client id mismatch",
			"driftType", KubeletIdentityDrift,
			"expectedKubeletIdentityClientID", opts.KubeletIdentityClientID,
			"actualKubeletIdentityClientID", kubeletIdentityClientID)
		return KubeletIdentityDrift, nil
	}

	return "", nil
}

func (c *CloudProvider) getNodeForDrift(ctx context.Context, nodeClaim *karpv1.NodeClaim) (*v1.Node, error) {
	logger := log.FromContext(ctx)

	n, err := corenodeclaimutils.NodeForNodeClaim(ctx, c.kubeClient, nodeClaim)
	if err != nil {
		if corenodeclaimutils.IsNodeNotFoundError(err) {
			// We do not return an error here as its expected within the lifecycle of the nodeclaims registration.
			// Core's checks only for Launched status which means we've started the create, but the node doesn't nessicarially exist yet
			// https://github.com/kubernetes-sigs/karpenter/blob/9877cf639e665eadcae9e46e5a702a1b30ced1d3/pkg/controllers/nodeclaim/disruption/drift.go#L51
			return nil, nil
		}
		if corenodeclaimutils.IsDuplicateNodeError(err) {
			logger.Info("duplicate node error detected, invariant violated")
		}
		return nil, err
	}
	if !n.DeletionTimestamp.IsZero() {
		// We do not need to check for drift if the node is being deleted.
		return nil, nil
	}

	return n, nil
}

// isMachineDrifted checks the DriftAction field of the AKS machine to determine if drift exists
func (c *CloudProvider) isMachineDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, _ *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)
	aksMachineName, isAKSMachine := instance.GetAKSMachineNameFromNodeClaim(nodeClaim)
	if !isAKSMachine {
		// Not an AKS machine node, no drift action to check
		logger.V(1).Info("no AKS machine name found, skipping drift action check", "nodeClaim", nodeClaim.Name)
		return "", nil
	}

	aksMachine, err := c.aksMachineInstanceProvider.Get(ctx, aksMachineName)
	if err != nil {
		return "", err
	}
	if aksMachine == nil {
		return "", fmt.Errorf("AKS machine with name %s not found", aksMachineName)
	}

	if aksMachine.Properties != nil && aksMachine.Properties.Status != nil && aksMachine.Properties.Status.DriftAction != nil {
		driftAction := lo.FromPtr(aksMachine.Properties.Status.DriftAction)
		driftReason := "" // Note: this is not being incorporated yet, and we currently return ClusterConfigDrift for all reasons. // Suggestion: could be extended.
		if aksMachine.Properties.Status.DriftReason != nil {
			driftReason = lo.FromPtr(aksMachine.Properties.Status.DriftReason)
		}

		switch driftAction {
		case "":
			return "", nil
		case armcontainerservice.DriftActionSynced:
			return "", nil
		case armcontainerservice.DriftActionRecreate:
			return ClusterConfigDrift, nil
		default:
			// AKS machine API may add additional drift actions in the future (e.g., restart, reimage). Karpenter (core) need to support them explicitly.
			// Meanwhile, re-create covers all cases.
			logger.Error(fmt.Errorf("unknown drift action %s for AKS machine %s", driftAction, aksMachineName), "unknown drift action, considering it as drift",
				"aksMachineName", aksMachineName,
				"driftAction", driftAction,
				"driftReason", driftReason)
			return ClusterConfigDrift, nil
		}
	}

	return "", nil
}
