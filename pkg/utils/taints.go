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

package utils

import (
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// ExtractTaints returns the general taints and startup taints from a NodeClaim,
// ensuring that UnregisteredNoExecuteTaint is present in startup taints.
func ExtractTaints(nodeClaim *karpv1.NodeClaim) (generalTaints, startupTaints []corev1.Taint) {
	generalTaints = nodeClaim.Spec.Taints
	startupTaints = nodeClaim.Spec.StartupTaints

	allTaints := lo.Flatten([][]corev1.Taint{generalTaints, startupTaints})

	// Ensure UnregisteredNoExecuteTaint is present
	if _, found := lo.Find(allTaints, func(t corev1.Taint) bool {
		return t.MatchTaint(&karpv1.UnregisteredNoExecuteTaint)
	}); !found {
		startupTaints = append(startupTaints, karpv1.UnregisteredNoExecuteTaint)
	}

	return generalTaints, startupTaints
}
