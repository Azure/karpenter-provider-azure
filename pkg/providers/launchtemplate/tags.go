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

	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/samber/lo"
)

const (
	karpenterManagedTagKey = "karpenter.azure.com_cluster"
)

var (
	NodePoolTagKey = strings.ReplaceAll(v1.NodePoolLabelKey, "/", "_")
)

// TODO: Would like to refactor this out of launchtemplate at some point
// Tags returns the tags to be applied to a resource (VM, Disk, NIC, etc)
func Tags(
	options *options.Options,
	nodeClass *v1beta1.AKSNodeClass,
	nodeClaim *v1.NodeClaim,
) map[string]*string {
	// TODO: This may not quite work because we assign some additional labels during the creation
	// of the static parameters for the launch template, which we are not passing here
	defaultTags := map[string]string{
		karpenterManagedTagKey: options.ClusterName,
	}
	if val, ok := nodeClaim.Labels[v1.NodePoolLabelKey]; ok {
		defaultTags[NodePoolTagKey] = val
	}

	return lo.MapEntries(
		lo.Assign(options.AdditionalTags, nodeClass.Spec.Tags, defaultTags),
		func(key string, value string) (string, *string) {
			return strings.ReplaceAll(key, "/", "_"), lo.ToPtr(value)
		},
	)
}
