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

package events

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/events"
)

const (
	AsyncProvisioningReason   = "AsyncProvisioningError"
	NodeClassResolutionReason = "NodeClassResolutionError"
)

func NodePoolFailedToResolveNodeClass(nodePool *v1.NodePool) events.Event {
	return events.Event{
		InvolvedObject: nodePool,
		Type:           corev1.EventTypeWarning,
		Reason:         NodeClassResolutionReason,
		Message:        "Failed resolving NodeClass",
		DedupeValues:   []string{string(nodePool.UID)},
	}
}

func NodeClaimFailedToResolveNodeClass(nodeClaim *v1.NodeClaim) events.Event {
	return events.Event{
		InvolvedObject: nodeClaim,
		Type:           corev1.EventTypeWarning,
		Reason:         NodeClassResolutionReason,
		Message:        "Failed resolving NodeClass",
		DedupeValues:   []string{string(nodeClaim.UID)},
	}
}

func NodeClaimFailedToRegister(nodeClaim *v1.NodeClaim, err error) events.Event {
	return events.Event{
		InvolvedObject: nodeClaim,
		Type:           corev1.EventTypeWarning,
		Reason:         AsyncProvisioningReason,
		Message:        fmt.Sprintf("Failed to register: %s", truncateMessage(err.Error())),
		DedupeValues:   []string{string(nodeClaim.UID)},
	}
}

const truncateAt = 500

func truncateMessage(msg string) string {
	if len(msg) < truncateAt {
		return msg
	}
	return msg[:truncateAt] + "..."
}
