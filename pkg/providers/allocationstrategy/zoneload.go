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

package allocationstrategy

import (
	"context"

	"github.com/patrickmn/go-cache"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	azurecache "github.com/Azure/karpenter-provider-azure/pkg/cache"
)

// ZoneLoadTracker reports how many NodeClaims a NodePool currently has in each zone, so that zone
// selection can prefer the least loaded zone rather than picking uniformly at random. Zone
// balancing is best-effort: an empty or nil load leaves selection uniformly random.
type ZoneLoadTracker interface {
	// Load returns the current per-zone NodeClaim count for the NodePool.
	Load(ctx context.Context, nodePoolName string) map[string]int
	// Record remembers the zone picked for a NodeClaim until that NodeClaim is observable with a
	// zone label of its own, so concurrent launches account for each other's picks.
	Record(nodeClaimName, nodePoolName, zone string)
}

type nodeClaimZoneLoadTracker struct {
	kubeClient client.Client
	// inFlight maps NodeClaim name to the zone picked for it, covering the window between the pick
	// and the NodeClaim being observable with that zone as a label.
	inFlight *cache.Cache
}

type zoneSelection struct {
	nodePoolName string
	zone         string
}

func NewZoneLoadTracker(kubeClient client.Client) ZoneLoadTracker {
	return &nodeClaimZoneLoadTracker{
		kubeClient: kubeClient,
		inFlight:   cache.New(azurecache.InFlightZoneSelectionTTL, azurecache.DefaultCleanupInterval),
	}
}

func (t *nodeClaimZoneLoadTracker) Load(ctx context.Context, nodePoolName string) map[string]int {
	nodeClaims := &karpv1.NodeClaimList{}
	if err := t.kubeClient.List(ctx, nodeClaims, client.MatchingLabels{karpv1.NodePoolLabelKey: nodePoolName}); err != nil {
		// Zone balancing is an optimization, so degrade to random zone selection instead of failing the launch.
		log.FromContext(ctx).V(1).Info("unable to list nodeclaims for zone balancing, falling back to random zone selection", "error", err.Error())
		return nil
	}
	load := map[string]int{}
	counted := sets.New[string]()
	for i := range nodeClaims.Items {
		nodeClaim := &nodeClaims.Items[i]
		// Terminating NodeClaims are on their way out, so new launches balance against what will remain.
		if !nodeClaim.DeletionTimestamp.IsZero() {
			continue
		}
		counted.Insert(nodeClaim.Name)
		zone, ok := nodeClaim.Labels[corev1.LabelTopologyZone]
		if !ok {
			// Not launched yet, or launched but not labeled yet: fall back to the zone we picked for it.
			zone = t.inFlightZone(nodeClaim.Name)
		}
		if zone != "" {
			load[zone]++
		}
	}
	// A NodeClaim launched moments ago may not have reached the client's cache yet, so count those
	// from the in-flight picks, skipping any already counted above so nothing is counted twice.
	for name, item := range t.inFlight.Items() {
		selection, ok := item.Object.(zoneSelection)
		if !ok || selection.nodePoolName != nodePoolName || counted.Has(name) {
			continue
		}
		load[selection.zone]++
	}
	return load
}

func (t *nodeClaimZoneLoadTracker) Record(nodeClaimName, nodePoolName, zone string) {
	if nodeClaimName == "" || zone == "" {
		return
	}
	t.inFlight.SetDefault(nodeClaimName, zoneSelection{nodePoolName: nodePoolName, zone: zone})
}

func (t *nodeClaimZoneLoadTracker) inFlightZone(nodeClaimName string) string {
	if item, ok := t.inFlight.Get(nodeClaimName); ok {
		if selection, ok := item.(zoneSelection); ok {
			return selection.zone
		}
	}
	return ""
}
