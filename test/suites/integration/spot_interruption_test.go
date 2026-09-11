// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration_test

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/controllers/node/interruption"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"

	. "github.com/onsi/ginkgo/v2"
	"github.com/samber/lo"
)

// Azure Spot preemption is handled by the provider's own deadline-aware interruption controller rather
// than by node repair, so unlike the Repair Policy suite these specs do not need the NodeRepair feature
// gate and are not subject to its unhealthy-node circuit breaker.
var _ = Describe("Spot Interruption", func() {
	var selector labels.Selector
	var dep *appsv1.Deployment
	var numPods int

	BeforeEach(func() {
		numPods = 1
		dep = coretest.Deployment(coretest.DeploymentOptions{
			Replicas: int32(numPods),
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		})
		selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
	})

	DescribeTable("should drain and replace a node with a scheduled Spot eviction",
		func(notice func() string) {
			env.ExpectCreated(nodeClass, nodePool, dep)
			pod := env.EventuallyExpectHealthyPodCount(selector, numPods)[0]
			node := env.ExpectCreatedNodeCount("==", 1)[0]
			env.EventuallyExpectInitializedNodeCount("==", 1)

			node = common.ReplaceNodeConditions(node, corev1.NodeCondition{
				Type:               interruption.ConditionTypePreemptionScheduled,
				Status:             corev1.ConditionTrue,
				Reason:             interruption.ConditionReasonSpotEvictionIncoming,
				Message:            notice(),
				LastTransitionTime: metav1.Time{Time: time.Now()},
			})
			env.ExpectStatusUpdated(node)

			env.EventuallyExpectNotFound(pod, node)
			env.EventuallyExpectHealthyPodCount(selector, numPods)
		},
		Entry("with the notice Azure normally gives", func() string {
			return preemptionNotice(time.Now().Add(30 * time.Second))
		}),
		Entry("with a deadline that has already passed", func() string {
			return preemptionNotice(time.Now().Add(-time.Minute))
		}),
		// The deadline is unrecoverable, so the controller must fail closed and clean up immediately
		// rather than leave pods on a node Azure is reclaiming.
		Entry("with an unparseable notice", func() string { return "Preempt Scheduled: unknown" }),
	)
})

func preemptionNotice(notBefore time.Time) string {
	return fmt.Sprintf("Preempt Scheduled: %s. For more information, see https://aka.ms/aks-spot-eviction. EventId: A1B2C3D4-E5F6-4A7B-8C9D-0E1F2A3B4C5D",
		notBefore.UTC().Format(time.RFC1123))
}
