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

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	corenodeclaimutils "sigs.k8s.io/karpenter/pkg/utils/nodeclaim"
)

const (
	NodeClassDrift       cloudprovider.DriftReason = "NodeClassDrift"
	K8sVersionDrift      cloudprovider.DriftReason = "K8sVersionDrift"
	ImageDrift           cloudprovider.DriftReason = "ImageDrift"
	SubnetDrift          cloudprovider.DriftReason = "SubnetDrift"
	KubeletIdentityDrift cloudprovider.DriftReason = "KubeletIdentityDrift"

	// TODO (charliedmcb): Use this const across code and test locations which are signaling/checking for "no drift"
	NoDrift cloudprovider.DriftReason = ""
)

func (c *CloudProvider) isNodeClassDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	// TODO: if we find more expensive checks, such as reading VMs or NICs from Azure, are being duplicated between checks, we should
	//       produce a lazy at-most-once that allows a check to cache a value for later checks to read.
	checks := []func(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error){
		c.areStaticFieldsDrifted,
		c.isK8sVersionDrifted,
		c.isKubeletIdentityDrifted,
		c.isImageVersionDrifted,
		c.isSubnetDrifted,
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
		return "", nil //nolint:nilerr
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

// TODO (charliedmcb): remove nolint on gocyclo. Added for now in order to pass "make verify
// Was looking at a way to breakdown the function to pass gocyclo, but didn't feel like the best code.
// Feel reassessing this within the future with a potential minor refactor would be best to fix the gocyclo.
// nolint: gocyclo
func (c *CloudProvider) isImageVersionDrifted(
	ctx context.Context,
	nodeClaim *karpv1.NodeClaim,
	nodeClass *v1beta1.AKSNodeClass,
) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)

	id, err := nodeclaimutils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		//     I don't think we can get to Drift, until after ProviderID is set, so this should be fine/impossible.
		return "", err
	}

	vm, err := c.vmInstanceProvider.Get(ctx, id)
	if err != nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		//     I don't think we can get to Drift, until after ProviderID is set, so this should be a real issue.
		//     However, we may want to collect this with the other errors up a level as to not block other drift conditions.
		return "", err
	}
	if vm == nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		//     I don't think we can get to Drift, until after ProviderID is set, so this should be a real issue.
		//     However, we may want to collect this with the other errors up a level as to not block other drift conditions.
		return "", fmt.Errorf("vm with id %s missing", id)
	}

	if vm.Properties == nil ||
		vm.Properties.StorageProfile == nil ||
		vm.Properties.StorageProfile.ImageReference == nil {
		// TODO (charliedmcb): this seems like an error case to me, but maybe not one to hard fail on? Is it even possible?
		//     We may want to collect this with the other errors up a level as to not block other drift conditions.
		return "", nil
	}
	CIGID := lo.FromPtr(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID)
	SIGID := lo.FromPtr(vm.Properties.StorageProfile.ImageReference.ID)
	vmImageID := lo.Ternary(SIGID != "", SIGID, CIGID)

	nodeImages, err := nodeClass.GetImages()
	// Note: this differs from AWS, as they don't check for status readiness during Drift.
	if err != nil {
		// Note: we don't consider this a hard failure for drift if the Images are not ready to use, so we ignore returning the error here.
		// The stored Images must be ready to use if we are to calculate potential Drift based on them.
		// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found across all of IsDrifted.
		logger.Info("node image not ready, skipping drift check", "error", err)
		return "", nil //nolint:nilerr
	}
	if len(nodeImages) == 0 {
		// Note: this case shouldn't happen, since if there are no nodeImages, the ConditionTypeImagesReady should be false.
		//     However, if it would happen, we want this to error, as it means the NodeClass is in a state it can't provision nodes.
		return "", fmt.Errorf("no images exist for the given constraints")
	}

	for _, availableImage := range nodeImages {
		if availableImage.ID == vmImageID {
			return "", nil
		}
	}

	logger.V(1).Info("drift triggered as actual image id was not found in the set of currently available node images",
		"driftType", ImageDrift,
		"actualImageID", vmImageID)
	return ImageDrift, nil
}

// isSubnetDrifted returns drift if the nic for this nodeclaim does not match the expected subnet
func (c *CloudProvider) isSubnetDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1beta1.AKSNodeClass) (cloudprovider.DriftReason, error) {
	expectedSubnet := lo.Ternary(nodeClass.Spec.VNETSubnetID == nil, options.FromContext(ctx).SubnetID, lo.FromPtr(nodeClass.Spec.VNETSubnetID))
	nicName := instance.GenerateResourceName(nodeClaim.Name)

	// TODO: Refactor all of AzConfig to be part of options
	nic, err := c.vmInstanceProvider.GetNic(ctx, options.FromContext(ctx).NodeResourceGroup, nicName)
	if err != nil {
		if sdkerrors.IsNotFoundErr(err) {
			return "", nil
		}
		return "", err
	}
	nicSubnet := getSubnetFromPrimaryIPConfig(nic)
	if nicSubnet == "" {
		return "", fmt.Errorf("no subnet found for nic: %s", nicName)
	}
	if nicSubnet != expectedSubnet {
		return SubnetDrift, nil
	}
	return "", nil
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

func getSubnetFromPrimaryIPConfig(nic *armnetwork.Interface) string {
	for _, ipConfig := range nic.Properties.IPConfigurations {
		if ipConfig.Properties.Subnet != nil && lo.FromPtr(ipConfig.Properties.Primary) {
			return lo.FromPtr(ipConfig.Properties.Subnet.ID)
		}
	}
	return ""
}
