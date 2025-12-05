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
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

// These labels are defined here rather than v1beta1 because we do not support scheduling simulation
// on these labels
var (
	AKSLabelEBPFDataplane       = v1beta1.AKSLabelDomain + "/ebpf-dataplane"
	AKSLabelAzureCNIOverlay     = v1beta1.AKSLabelDomain + "/azure-cni-overlay"
	AKSLabelSubnetName          = v1beta1.AKSLabelDomain + "/network-subnet"
	AKSLabelVNetGUID            = v1beta1.AKSLabelDomain + "/nodenetwork-vnetguid"
	AKSLabelPodNetworkType      = v1beta1.AKSLabelDomain + "/podnetwork-type"
	AKSLabelNetworkStatelessCNI = v1beta1.AKSLabelDomain + "/network-stateless-cni"

	AKSLabelRole = v1beta1.AKSLabelDomain + "/role"

	kubeletLabelNamespaces = sets.NewString(
		v1.LabelNamespaceSuffixKubelet,
		v1.LabelNamespaceSuffixNode,
	)

	kubeletLabels = sets.NewString(
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

	K8sLabelDomains = sets.New(
		"kubernetes.io",
		"k8s.io",
	)
)

// These label definitions taken from here: https://github.com/kubernetes/kubernetes/blob/e319c541f144e9bee6160f1dd8671638a9029f4c/staging/src/k8s.io/kubelet/pkg/apis/well_known_labels.go#L67
const (
	// LabelOS is a label to indicate the operating system of the node.
	// The OS labels are promoted to GA in 1.14. kubelet applies GA labels and stop applying the beta OS labels in Kubernetes 1.19.
	LabelOS = "beta.kubernetes.io/os"
	// LabelArch is a label to indicate the architecture of the node.
	// The Arch labels are promoted to GA in 1.14. kubelet applies GA labels and stop applying the beta Arch labels in Kubernetes 1.19.
	LabelArch = "beta.kubernetes.io/arch"
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
	labels[v1beta1.AKSLabelCluster] = NormalizeClusterResourceGroupNameForLabel(opts.NodeResourceGroup)
	// Note that while we set the Kubelet identity label here, in bootstrap API mode, the actual kubelet identity that is set in the bootstrapping
	// script is configured by the NPS service. That means the label can be set to the older client ID if the client ID
	// changed recently. This is OK because drift will correct it.
	labels[v1beta1.AKSLabelKubeletIdentityClientID] = opts.KubeletIdentityClientID
	labels[v1beta1.AKSLabelMode] = v1beta1.ModeUser

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

// IsKubeletLabel returns true if the given label is a label kubelet is allowed to set.
// This is similar to the method one used by the node restriction admission
// https://github.com/kubernetes/kubernetes/blob/e319c541f144e9bee6160f1dd8671638a9029f4c/staging/src/k8s.io/kubelet/pkg/apis/well_known_labels.go#L67
// with the isKubernetesLabel check from https://github.com/kubernetes/kubernetes/blob/4bed36e03e7bd699b089d33da6f7d7c9ef9eb661/cmd/kubelet/app/options/options.go#L176C6-L176C23.
func IsKubeletLabel(key string) bool {
	if kubeletLabels.Has(key) {
		return true
	}

	namespace := getLabelNamespace(key)
	if !isKubernetesNamespace(namespace) {
		return true
	}

	for allowedNamespace := range kubeletLabelNamespaces {
		if namespace == allowedNamespace || strings.HasSuffix(namespace, "."+allowedNamespace) {
			return true
		}
	}

	return false
}

// GetWellKnownSingleValuedRequirementLabels converts well-known Azure single-value instanceType.Requirements to labels
// This is useful for projecting requirements from the NodeClaim to labels, which is required for scheduling simulation to work
// correctly.
func GetWellKnownSingleValuedRequirementLabels(requirements scheduling.Requirements) map[string]string {
	wellKnown := func(k string, r *scheduling.Requirement) bool {
		return v1beta1.AzureWellKnownLabels.Has(k)
	}
	return GetFilteredSingleValuedRequirementLabels(requirements, wellKnown)
}

// GetAllSingleValuedRequirementLabels converts single-value instanceType.Requirements to labels
// Like   instanceType.Requirements.Labels() it uses single-valued requirements
// Unlike instanceType.Requirements.Labels() it does not filter out restricted Node labels
func GetAllSingleValuedRequirementLabels(requirements scheduling.Requirements) map[string]string {
	all := func(k string, r *scheduling.Requirement) bool {
		return true
	}
	return GetFilteredSingleValuedRequirementLabels(requirements, all)
}

func GetFilteredSingleValuedRequirementLabels(requirements scheduling.Requirements, predicate func(k string, r *scheduling.Requirement) bool) map[string]string {
	labels := map[string]string{}
	if len(requirements) == 0 {
		return labels
	}
	for key, req := range requirements {
		if req.Len() == 1 && predicate(key, req) {
			labels[key] = req.Values()[0]
		}
	}
	return labels
}

// isKubernetesNamespace checks if the given namespace belongs to kubernetes.io or k8s.io domains
// Similar to https://github.com/kubernetes/kubernetes/blob/4bed36e03e7bd699b089d33da6f7d7c9ef9eb661/cmd/kubelet/app/options/options.go#L176C6-L176C23
func isKubernetesNamespace(namespace string) bool {
	for k8sDomain := range K8sLabelDomains {
		if namespace == k8sDomain || strings.HasSuffix(namespace, "."+k8sDomain) {
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

func NormalizeClusterResourceGroupNameForLabel(resourceGroupName string) string {
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
