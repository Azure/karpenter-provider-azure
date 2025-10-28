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

package labels

import (
	"context"
	"strconv"
	"strings"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
)

var (
	AKSLabelEBPFDataplane       = v1beta1.AKSLabelDomain + "/ebpf-dataplane"
	AKSLabelAzureCNIOverlay     = v1beta1.AKSLabelDomain + "/azure-cni-overlay"
	AKSLabelSubnetName          = v1beta1.AKSLabelDomain + "/network-subnet"
	AKSLabelVNetGUID            = v1beta1.AKSLabelDomain + "/nodenetwork-vnetguid"
	AKSLabelPodNetworkType      = v1beta1.AKSLabelDomain + "/podnetwork-type"
	AKSLabelNetworkStatelessCNI = v1beta1.AKSLabelDomain + "/network-stateless-cni"

	AKSLabelRole    = v1beta1.AKSLabelDomain + "/role"
	AKSLabelCluster = v1beta1.AKSLabelDomain + "/cluster"
)

func Get(
	ctx context.Context,
	nodeClass *v1beta1.AKSNodeClass,
) (map[string]string, error) {
	labels := map[string]string{}
	opts := options.FromContext(ctx)

	subnetID := lo.Ternary(nodeClass.Spec.VNETSubnetID != nil, lo.FromPtr(nodeClass.Spec.VNETSubnetID), opts.SubnetID)

	// Add labels that are always there
	labels[AKSLabelRole] = "agent"
	labels[AKSLabelCluster] = normalizeResourceGroupNameForLabel(opts.NodeResourceGroup)
	// Note that while we set the Kubelet identity label here, in bootstrap API mode, the actual kubelet identity that is set in the bootstrapping
	// script is configured by the NPS service. That means the label can be set to the older client ID if the client ID
	// changed recently. This is OK because drift will correct it.
	labels[v1beta1.AKSLabelKubeletIdentityClientID] = opts.KubeletIdentityClientID
	labels["kubernetes.azure.com/mode"] = "user"

	if opts.IsAzureCNIOverlay() {
		// TODO: make conditional on pod subnet
		kubernetesVersion, err := nodeClass.GetKubernetesVersion()
		if err != nil {
			return nil, err
		}

		vnetSubnetComponents, err := utils.GetVnetSubnetIDComponents(subnetID) // good
		if err != nil {
			return nil, err
		}
		labels[AKSLabelSubnetName] = vnetSubnetComponents.SubnetName
		labels[AKSLabelVNetGUID] = options.FromContext(ctx).VnetGUID
		labels[AKSLabelAzureCNIOverlay] = strconv.FormatBool(true)
		labels[AKSLabelPodNetworkType] = consts.NetworkPluginModeOverlay

		parsedVersion, err := semver.ParseTolerant(strings.TrimPrefix(kubernetesVersion, "v"))
		// Sanity Check: in production we should always have a k8s version set
		if err != nil {
			return nil, err
		}
		labels[AKSLabelNetworkStatelessCNI] = lo.Ternary(parsedVersion.GE(semver.Version{Major: 1, Minor: 34}), "true", "false")
	}
	if opts.NetworkDataplane == consts.NetworkDataplaneCilium {
		// This label is required for the cilium agent daemonset because
		// we select the nodes for the daemonset based on this label
		//              - key: kubernetes.azure.com/ebpf-dataplane
		//            operator: In
		//            values:
		//              - cilium

		labels[AKSLabelEBPFDataplane] = consts.NetworkDataplaneCilium
	}

	return labels, nil
}

func normalizeResourceGroupNameForLabel(resourceGroupName string) string {
	truncated := resourceGroupName
	truncated = strings.ReplaceAll(truncated, "(", "-")
	truncated = strings.ReplaceAll(truncated, ")", "-")
	const maxLen = 63
	if len(truncated) > maxLen {
		truncated = truncated[0:maxLen]
	}

	if strings.HasSuffix(truncated, "-") ||
		strings.HasSuffix(truncated, "_") ||
		strings.HasSuffix(truncated, ".") {
		if len(truncated) > 62 {
			return truncated[0:len(truncated)-1] + "z"
		}
		return truncated + "z"
	}
	return truncated
}
