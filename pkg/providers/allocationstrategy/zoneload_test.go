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

package allocationstrategy_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/allocationstrategy"
)

func TestZoneLoadTracker_CountsNodeClaimsOfNodePoolByZone(t *testing.T) {
	g := NewWithT(t)
	tracker := allocationstrategy.NewZoneLoadTracker(fake.NewClientBuilder().WithObjects(
		testNodeClaim("nodeclaim-1", "default", "westus-1"),
		testNodeClaim("nodeclaim-2", "default", "westus-1"),
		testNodeClaim("nodeclaim-3", "default", "westus-2"),
		testNodeClaim("nodeclaim-4", "other", "westus-3"),
	).Build())

	g.Expect(tracker.Load(context.Background(), "default")).To(Equal(map[string]int{"westus-1": 2, "westus-2": 1}))
}

func TestZoneLoadTracker_IgnoresTerminatingNodeClaims(t *testing.T) {
	g := NewWithT(t)
	terminating := testNodeClaim("nodeclaim-2", "default", "westus-2")
	terminating.Finalizers = []string{"karpenter.sh/termination"}
	terminating.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	tracker := allocationstrategy.NewZoneLoadTracker(fake.NewClientBuilder().WithObjects(
		testNodeClaim("nodeclaim-1", "default", "westus-1"),
		terminating,
	).Build())

	g.Expect(tracker.Load(context.Background(), "default")).To(Equal(map[string]int{"westus-1": 1}))
}

func TestZoneLoadTracker_UsesRecordedZoneForUnlabeledNodeClaims(t *testing.T) {
	g := NewWithT(t)
	tracker := allocationstrategy.NewZoneLoadTracker(fake.NewClientBuilder().WithObjects(
		testNodeClaim("nodeclaim-1", "default", ""),
	).Build())

	g.Expect(tracker.Load(context.Background(), "default")).To(BeEmpty())
	tracker.Record("nodeclaim-1", "default", "westus-2")
	g.Expect(tracker.Load(context.Background(), "default")).To(Equal(map[string]int{"westus-2": 1}))
}

func TestZoneLoadTracker_DoesNotDoubleCountRecordedNodeClaims(t *testing.T) {
	g := NewWithT(t)
	tracker := allocationstrategy.NewZoneLoadTracker(fake.NewClientBuilder().WithObjects(
		testNodeClaim("nodeclaim-1", "default", "westus-1"),
	).Build())

	tracker.Record("nodeclaim-1", "default", "westus-1")
	g.Expect(tracker.Load(context.Background(), "default")).To(Equal(map[string]int{"westus-1": 1}))
}

func TestZoneLoadTracker_CountsRecordedNodeClaimsMissingFromClient(t *testing.T) {
	g := NewWithT(t)
	tracker := allocationstrategy.NewZoneLoadTracker(fake.NewClientBuilder().Build())

	tracker.Record("nodeclaim-1", "default", "westus-1")
	tracker.Record("nodeclaim-2", "other", "westus-2")
	g.Expect(tracker.Load(context.Background(), "default")).To(Equal(map[string]int{"westus-1": 1}))
}

func testNodeClaim(name, nodePoolName, zone string) *karpv1.NodeClaim {
	nodeClaim := &karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{karpv1.NodePoolLabelKey: nodePoolName},
		},
	}
	if zone != "" {
		nodeClaim.Labels[corev1.LabelTopologyZone] = zone
	}
	return nodeClaim
}
