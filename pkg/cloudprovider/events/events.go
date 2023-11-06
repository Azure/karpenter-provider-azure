// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package events

import (
	v1 "k8s.io/api/core/v1"

	"github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/events"
)

func NodePoolFailedToResolveNodeClass(nodePool *v1beta1.NodePool) events.Event {
	return events.Event{
		InvolvedObject: nodePool,
		Type:           v1.EventTypeWarning,
		Message:        "Failed resolving AKSNodeClass",
		DedupeValues:   []string{string(nodePool.UID)},
	}
}

func NodeClaimFailedToResolveNodeClass(nodeClaim *v1beta1.NodeClaim) events.Event {
	return events.Event{
		InvolvedObject: nodeClaim,
		Type:           v1.EventTypeWarning,
		Message:        "Failed resolving AKSNodeClass",
		DedupeValues:   []string{string(nodeClaim.UID)},
	}
}
