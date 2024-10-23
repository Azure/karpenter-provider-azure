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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/samber/lo"
	"knative.dev/pkg/logging"

	v1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

const (
	K8sVersionDrift   cloudprovider.DriftReason = "K8sVersionDrift"
	ImageVersionDrift cloudprovider.DriftReason = "ImageVersionDrift"
	SubnetDrift       cloudprovider.DriftReason = "SubnetDrift"
)

func (c *CloudProvider) isK8sVersionDrifted(ctx context.Context, nodeClaim *corev1beta1.NodeClaim) (cloudprovider.DriftReason, error) {
	logger := logging.FromContext(ctx)

	nodeName := nodeClaim.Status.NodeName

	if nodeName == "" {
		return "", nil
	}

	n := &v1.Node{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{Name: nodeName}, n); err != nil {
		// TODO (charliedmcb): should we ignore is not found errors? Will it ever be trying to check for drift before the node/vm exist?
		return "", err
	}

	k8sVersion, err := c.imageProvider.KubeServerVersion(ctx)
	if err != nil {
		return "", err
	}

	nodeK8sVersion := strings.TrimPrefix(n.Status.NodeInfo.KubeletVersion, "v")
	if nodeK8sVersion != k8sVersion {
		logger.Debugf("drift triggered for %s, with expected k8s version %s, and actual k8s version %s", K8sVersionDrift, k8sVersion, nodeK8sVersion)
		return K8sVersionDrift, nil
	}
	return "", nil
}

// TODO (charliedmcb): remove nolint on gocyclo. Added for now in order to pass "make verify
// Was looking at a way to breakdown the function to pass gocyclo, but didn't feel like the best code.
// Feel reassessing this within the future with a potential minor refactor would be best to fix the gocyclo.
// nolint: gocyclo
func (c *CloudProvider) isImageVersionDrifted(
	ctx context.Context, nodeClaim *corev1beta1.NodeClaim) (cloudprovider.DriftReason, error) {
	logger := logging.FromContext(ctx)

	id, err := utils.GetVMName(nodeClaim.Status.ProviderID)
	if err != nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		return "", err
	}

	vm, err := c.instanceProvider.Get(ctx, id)
	if err != nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		return "", err
	}
	if vm == nil {
		// TODO (charliedmcb): Do we need to handle vm not found here before its provisioned?
		return "", fmt.Errorf("vm with id %s missing", id)
	}

	if vm.Properties == nil ||
		vm.Properties.StorageProfile == nil ||
		vm.Properties.StorageProfile.ImageReference == nil ||
		vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID == nil ||
		*vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID == "" {
		logger.Debug("not using a CommunityGalleryImageID for nodeClaim %s", nodeClaim.Name)
		return "", nil
	}

	vmImageID := *vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID

	publicGalleryURL, communityImageName, _, err := imagefamily.ParseCommunityImageIDInfo(vmImageID)
	if err != nil {
		return "", err
	}

	expectedImageID, err := c.imageProvider.GetImageID(ctx, communityImageName, publicGalleryURL)
	if err != nil {
		return "", err
	}

	if vmImageID != expectedImageID {
		logger.Debugf("drift triggered for %s, with expected image id %s, and actual image id %s", ImageVersionDrift, expectedImageID, vmImageID)
		return ImageVersionDrift, nil
	}
	return "", nil
}

// isSubnetDrifted returns drift if the nic for this nodeclaim does not match the expected subnet
func (c *CloudProvider) isSubnetDrifted(ctx context.Context, nodeClaim *corev1beta1.NodeClaim, nodeClass *v1alpha2.AKSNodeClass) (cloudprovider.DriftReason, error) {
	expectedSubnet := lo.Ternary(nodeClass.Spec.VNETSubnetID == nil, options.FromContext(ctx).SubnetID, lo.FromPtr(nodeClass.Spec.VNETSubnetID))
	nicName := instance.GenerateResourceName(nodeClaim.Name)

	// TODO: Refactor all of AzConfig to be part of options
	nic, err := instance.GetNic(ctx, c.instanceProvider.AZClient.NetworkInterfacesClient, options.FromContext(ctx).NodeResourceGroup, nicName)
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
