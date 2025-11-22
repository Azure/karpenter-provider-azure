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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
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

// This these label definitions taken from here: https://github.com/kubernetes/kubernetes/blob/e319c541f144e9bee6160f1dd8671638a9029f4c/staging/src/k8s.io/kubelet/pkg/apis/well_known_labels.go#L67
const (
	// LabelOS is a label to indicate the operating system of the node.
	// The OS labels are promoted to GA in 1.14. kubelet applies GA labels and stop applying the beta OS labels in Kubernetes 1.19.
	LabelOS = "beta.kubernetes.io/os"
	// LabelArch is a label to indicate the architecture of the node.
	// The Arch labels are promoted to GA in 1.14. kubelet applies GA labels and stop applying the beta Arch labels in Kubernetes 1.19.
	LabelArch = "beta.kubernetes.io/arch"
)

var kubeletLabelNamespaces = sets.NewString(
	v1.LabelNamespaceSuffixKubelet,
	v1.LabelNamespaceSuffixNode,
)

var kubeletLabels = sets.NewString(
	v1.LabelHostname,
	v1.LabelTopologyZone,
	v1.LabelTopologyRegion,
	v1.LabelFailureDomainBetaZone,
	v1.LabelFailureDomainBetaRegion,
	v1.LabelInstanceType,
	v1.LabelInstanceTypeStable,
	v1.LabelOSStable,
	v1.LabelArchStable,

	LabelOS,
	LabelArch,
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

// This was inspired by the method added here: https://github.com/kubernetes-sigs/karpenter/pull/2586/files
// but does not restrict WellKnownLabels (which means this is really the same as )
// the one used by the node restriction admission https://github.com/kubernetes/kubernetes/blob/e319c541f144e9bee6160f1dd8671638a9029f4c/staging/src/k8s.io/kubelet/pkg/apis/well_known_labels.go#L67
func IsKubeletLabel(key string) bool {
	if kubeletLabels.Has(key) {
		return true
	}

	namespace := getLabelNamespace(key)
	for allowedNamespace := range kubeletLabelNamespaces {
		if namespace == allowedNamespace || strings.HasSuffix(namespace, "."+allowedNamespace) {
			return true
		}
	}

	return false
}

func getLabelNamespace(key string) string {
	if parts := strings.SplitN(key, "/", 2); len(parts) == 2 {
		return parts[0]
	}
	return ""
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
