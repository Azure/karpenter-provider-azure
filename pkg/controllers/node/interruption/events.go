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

package interruption

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/karpenter/pkg/events"
)

// SpotInterrupted records that Azure is reclaiming the node's Spot capacity and that Karpenter is
// draining it against the published deadline.
func SpotInterrupted(node *corev1.Node, deadline time.Time) events.Event {
	return events.Event{
		InvolvedObject: node,
		Type:           corev1.EventTypeWarning,
		Reason:         "SpotInterrupted",
		Message: fmt.Sprintf("Azure Spot eviction scheduled, draining node before %s",
			deadline.UTC().Format(time.RFC3339)),
		DedupeValues: []string{string(node.UID)},
	}
}

// UnknownSpotEvictionDeadline records that a preemption notice arrived without a usable deadline. The
// node is still cleaned up, but with no drain budget at all, so this is actionable: it means the
// condition message format changed and the parser needs updating.
func UnknownSpotEvictionDeadline(node *corev1.Node, message string) events.Event {
	return events.Event{
		InvolvedObject: node,
		Type:           corev1.EventTypeWarning,
		Reason:         "UnknownSpotEvictionDeadline",
		Message: fmt.Sprintf("Azure Spot eviction scheduled but no deadline could be parsed from %q, "+
			"draining node immediately", message),
		DedupeValues: []string{string(node.UID)},
	}
}
