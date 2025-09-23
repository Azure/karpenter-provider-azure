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

package launchtemplate

import (
	"strings"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/samber/lo"
)

const (
	KarpenterManagedTagKey = "karpenter.azure.com_cluster"
)

var (
	NodePoolTagKey                     = strings.ReplaceAll(karpv1.NodePoolLabelKey, "/", "_")
	KarpenterAKSMachineNodeClaimTagKey = "karpenter.azure.com_aksmachine_nodeclaim"
)

// TODO: Would like to refactor this out of launchtemplate at some point
// Tags returns the tags to be applied to a resource (VM, Disk, NIC, etc)
func Tags(
	options *options.Options,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *karpv1.NodeClaim,
) map[string]*string {
	defaultTags := map[string]string{
		KarpenterManagedTagKey: options.ClusterName,
	}
	// Note: Be careful depending on nodeClaim.Labels here, as we assign some additional labels during the creation
	// of the static parameters for the launch template. Those labels haven't actually been applied to the nodeClaim yet,
	// so if you try to use them here you will find they are missing.
	// For now, we only depend on labels that are added to the nodeClaim itself.
	if val, ok := nodeClaim.Labels[karpv1.NodePoolLabelKey]; ok {
		defaultTags[NodePoolTagKey] = val
	}

	// MapEntries first so that karpenter.azure.com_cluster and karpenter.azure.com/cluster collide
	additionalTags := lo.MapEntries(options.AdditionalTags, mapTags)
	nodeClassTags := lo.MapEntries(nodeClass.Spec.Tags, mapTags)
	defaultTagsMapped := lo.MapEntries(defaultTags, mapTags)

	return lo.Assign(additionalTags, nodeClassTags, defaultTagsMapped)
}

func mapTags(key string, value string) (string, *string) {
	return strings.ReplaceAll(key, "/", "_"), lo.ToPtr(value)
}
