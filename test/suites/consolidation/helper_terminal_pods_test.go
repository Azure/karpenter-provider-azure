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

package consolidation_test

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// terminalPodCleanupTarget identifies an owner, not a serving rollout. Generation,
// template, replica status and readiness are deliberately not cleanup inputs.
type terminalPodCleanupTarget struct {
	key types.NamespacedName
	uid types.UID
}

// normalizeTerminalDeploymentPods is an intentionally no-op regression seam.
// The delete-consolidation fixture currently leaves terminal Pod objects in place.
// This seam remains unwired from the live suite while standard tests expose the
// missing ownership-fenced cleanup, evidence preservation and absence checks.
func normalizeTerminalDeploymentPods(_ context.Context, _ kubernetes.Interface, _ terminalPodCleanupTarget, _ func(*corev1.Pod) error) error {
	return nil
}
