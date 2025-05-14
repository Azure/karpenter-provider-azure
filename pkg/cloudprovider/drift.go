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
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

const (
	NodeClassDrift    cloudprovider.DriftReason = "NodeClassDrift"
	K8sVersionDrift   cloudprovider.DriftReason = "K8sVersionDrift"
	ImageVersionDrift cloudprovider.DriftReason = "ImageVersionDrift" // TODO (chmcbrid): should we change this to ImageDrift, since it covers more than just "ImageVersion"?
	SubnetDrift       cloudprovider.DriftReason = "SubnetDrift"

	// TODO (charliedmcb): Use this const across code and test locations which are signaling/checking for "no drift"
	NoDrift cloudprovider.DriftReason = ""
)

func (c *CloudProvider) isNodeClassDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) (cloudprovider.DriftReason, error) {
	// First check if the node class is statically staticFieldsDrifted to save on API calls.
	if staticFieldsDrifted := c.areStaticFieldsDrifted(nodeClaim, nodeClass); staticFieldsDrifted != "" {
		return staticFieldsDrifted, nil
	}
	k8sVersionDrifted, err := c.isK8sVersionDrifted(ctx, nodeClaim, nodeClass)
	if err != nil {
		return "", err
	}
	if k8sVersionDrifted != "" {
		return k8sVersionDrifted, nil
	}
	imageVersionDrifted, err := c.isImageVersionDrifted(ctx, nodeClaim, nodeClass)
	if err != nil {
		return "", err
	}
	if imageVersionDrifted != "" {
		return imageVersionDrifted, nil
	}
	subnetDrifted, err := c.isSubnetDrifted(ctx, nodeClaim, nodeClass)
	if err != nil {
		return "", err
	}
	if subnetDrifted != "" {
		return subnetDrifted, nil
	}
	return "", nil
}

func (c *CloudProvider) areStaticFieldsDrifted(nodeClaim *karpv1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) cloudprovider.DriftReason {
	nodeClassHash, foundNodeClassHash := nodeClass.Annotations[v1alpha2.AnnotationAKSNodeClassHash]
	nodeClassHashVersion, foundNodeClassHashVersion := nodeClass.Annotations[v1alpha2.AnnotationAKSNodeClassHashVersion]
	nodeClaimHash, foundNodeClaimHash := nodeClaim.Annotations[v1alpha2.AnnotationAKSNodeClassHash]
	nodeClaimHashVersion, foundNodeClaimHashVersion := nodeClaim.Annotations[v1alpha2.AnnotationAKSNodeClassHashVersion]

	if !foundNodeClassHash || !foundNodeClaimHash || !foundNodeClassHashVersion || !foundNodeClaimHashVersion {
		return ""
	}
	// validate that the hash version for the AKSNodeClass is the same as the NodeClaim before evaluating for static drift
	if nodeClassHashVersion != nodeClaimHashVersion {
		return ""
	}
	return lo.Ternary(nodeClassHash != nodeClaimHash, NodeClassDrift, "")
}

func (c *CloudProvider) isK8sVersionDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)

	k8sVersion, err := nodeClass.GetKubernetesVersion()
	// Note: this differs from AWS, as they don't check for status readiness during Drift.
	if err != nil {
		// Note: we don't consider this a hard failure for drift if the KubernetesVersion is invalid/not ready to use, so we ignore returning the error here.
		// We simply ensure the stored version is valid and ready to use, if we are to calculate potential Drift based on it.
		// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found across all of IsDrifted.
		logger.Info(fmt.Sprintf("WARN: Kubernetes version readiness invalid when checking drift: %s", err))
		return "", nil //nolint:nilerr
	}

	nodeName := nodeClaim.Status.NodeName
	if nodeName == "" {
		// We do not return an error here as its expected within the lifecycle of the nodeclaims registration.
		// Drift can be called for a nodeclaim once its launched, but .Status.NodeName is only filled out after the node is registered:
		// https://github.com/kubernetes-sigs/karpenter/blob/8b9ea2e7cd10acdb40bccdf91a153a2e69b71107/pkg/controllers/nodeclaim/lifecycle/registration.go#L83
		return "", nil
	}

	n := &v1.Node{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{Name: nodeName}, n); err != nil {
		// Core's check for Launched status should currently prevent us from getting here before the node exists because of the LRO block on Create:
		// https://github.com/kubernetes-sigs/karpenter/blob/9877cf639e665eadcae9e46e5a702a1b30ced1d3/pkg/controllers/nodeclaim/disruption/drift.go#L51
		// However, in my opinion, we should look at updating this logic to ignore NotFound errors as we fix the LRO issue.
		// Shouldn't cause an issue to my awareness, but could be noisy.
		// TODO: re-evaluate ignoring NotFound error, and using core's library for nodeclaims. Similar to usage here:
		// https://github.com/kubernetes-sigs/karpenter/blob/bbe6bd27e65d88fe55376b6c3c2c828312c105c4/pkg/controllers/nodeclaim/lifecycle/registration.go#L53
		return "", err
	}
	nodeK8sVersion := strings.TrimPrefix(n.Status.NodeInfo.KubeletVersion, "v")

	if nodeK8sVersion != k8sVersion {
		logger.V(1).Info(fmt.Sprintf("drift triggered for %s, with expected k8s version %s, and actual k8s version %s", K8sVersionDrift, k8sVersion, nodeK8sVersion))
		return K8sVersionDrift, nil
	}
	return "", nil
}

// TODO (charliedmcb): remove nolint on gocyclo. Added for now in order to pass "make verify
// Was looking at a way to breakdown the function to pass gocyclo, but didn't feel like the best code.
// Feel reassessing this within the future with a potential minor refactor would be best to fix the gocyclo.
// nolint: gocyclo
func (c *CloudProvider) isImageVersionDrifted(
	ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) (cloudprovider.DriftReason, error) {
	logger := log.FromContext(ctx)

	id, err := utils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		//     I don't think we can get to Drift, until after ProviderID is set, so this should be fine/impossible.
		return "", err
	}

	vm, err := c.instanceProvider.Get(ctx, id)
	if err != nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		//     I don't think we can get to Drift, until after ProviderID is set, so this should be a real issue.
		//     However, we may want to collect this with the over errors up a level as to not block over drift conditions.
		return "", err
	}
	if vm == nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		//     I don't think we can get to Drift, until after ProviderID is set, so this should be a real issue.
		//     However, we may want to collect this with the over errors up a level as to not block over drift conditions.
		return "", fmt.Errorf("vm with id %s missing", id)
	}

	if vm.Properties == nil ||
		vm.Properties.StorageProfile == nil ||
		vm.Properties.StorageProfile.ImageReference == nil {
		// TODO (charliedmcb): this seems like an error case to me, but maybe not one to hard fail on? Is it even possible?
		//     We may want to collect this with the over errors up a level as to not block over drift conditions.
		return "", nil
	}
	CIGID := lo.FromPtr(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID)
	SIGID := lo.FromPtr(vm.Properties.StorageProfile.ImageReference.ID)
	vmImageID := lo.Ternary(SIGID != "", SIGID, CIGID)

	nodeImages, err := nodeClass.GetImages()
	// Note: this differs from AWS, as they don't check for status readiness during Drift.
	if err != nil {
		// Note: we don't consider this a hard failure for drift if the Images are not ready to use, so we ignore returning the error here.
		// We simply ensure the stored Images are ready to use, if we are to calculate potential Drift based on them.
		// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found across all of IsDrifted.
		logger.Info(fmt.Sprintf("WARN: NodeImage readiness invalid when checking drift: %s", err))
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

	logger.V(1).Info(fmt.Sprintf("drift triggered for %s, as actual image id %s was not found in the set of currently available node images", ImageVersionDrift, vmImageID))
	return ImageVersionDrift, nil
}

// isSubnetDrifted returns drift if the nic for this nodeclaim does not match the expected subnet
func (c *CloudProvider) isSubnetDrifted(ctx context.Context, nodeClaim *karpv1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) (cloudprovider.DriftReason, error) {
	expectedSubnet := lo.Ternary(nodeClass.Spec.VNETSubnetID == nil, options.FromContext(ctx).SubnetID, lo.FromPtr(nodeClass.Spec.VNETSubnetID))
	nicName := instance.GenerateResourceName(nodeClaim.Name)

	// TODO: Refactor all of AzConfig to be part of options
	nic, err := c.instanceProvider.GetNic(ctx, options.FromContext(ctx).NodeResourceGroup, nicName)
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

func getSubnetFromPrimaryIPConfig(nic *armnetwork.Interface) string {
	for _, ipConfig := range nic.Properties.IPConfigurations {
		if ipConfig.Properties.Subnet != nil && lo.FromPtr(ipConfig.Properties.Primary) {
			return lo.FromPtr(ipConfig.Properties.Subnet.ID)
		}
	}
	return ""
}
