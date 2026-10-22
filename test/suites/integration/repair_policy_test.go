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
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("Repair Policy", func() {
	var selector labels.Selector
	var dep *appsv1.Deployment
	var numPods int
	var unhealthyCondition corev1.NodeCondition

	BeforeEach(func() {
		// remove this if NodeRepair feature gate is enabled by default
		if env.InClusterController {
			env.ExpectSettingsOverridden(corev1.EnvVar{Name: "FEATURE_GATES", Value: "NodeRepair=True"})
		} else {
			Skip("This test requires the controller to be running in-cluster (to ensure NodeRepair feature gate is enabled")
		}

		unhealthyCondition = corev1.NodeCondition{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: time.Now().Add(-11 * time.Minute)},
		}
		numPods = 1
		// Add pods with a do-not-disrupt annotation so that we can check node metadata before we disrupt
		dep = coretest.Deployment(coretest.DeploymentOptions{
			Replicas: int32(numPods),
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "my-app",
					},
					Annotations: map[string]string{
						karpenterv1.DoNotDisruptAnnotationKey: "true",
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		})

		selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
	})

	DescribeTable("Conditions", func(unhealthyCondition corev1.NodeCondition) {
		env.ExpectCreated(nodeClass, nodePool, dep)
		pod := env.EventuallyExpectHealthyPodCount(selector, numPods)[0]
		node := env.ExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)

		node = common.ReplaceNodeConditions(node, unhealthyCondition)
		env.ExpectStatusUpdated(node)

		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, numPods)
	},
		Entry("Node Ready False", corev1.NodeCondition{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: time.Now().Add(-11 * time.Minute)},
		}),
		Entry("Node Ready Unknown", corev1.NodeCondition{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionUnknown,
			LastTransitionTime: metav1.Time{Time: time.Now().Add(-11 * time.Minute)},
		}),
		Entry("Node Healthy False", corev1.NodeCondition{
			Type:               corev1.NodeConditionType("kubernetes.azure.com/NodeHealthy"),
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: time.Now()},
		}),
	)
	It("should ignore disruption budgets", func() {
		nodePool.Spec.Disruption.Budgets = []karpenterv1.Budget{
			{
				Nodes: "0",
			},
		}
		env.ExpectCreated(nodeClass, nodePool, dep)
		pod := env.EventuallyExpectHealthyPodCount(selector, numPods)[0]
		node := env.ExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)

		node = common.ReplaceNodeConditions(node, unhealthyCondition)
		env.ExpectStatusUpdated(node)

		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, numPods)
	})
	It("should ignore do-not-disrupt annotation on node", func() {
		env.ExpectCreated(nodeClass, nodePool, dep)
		pod := env.EventuallyExpectHealthyPodCount(selector, numPods)[0]
		node := env.ExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)

		node.Annotations[karpenterv1.DoNotDisruptAnnotationKey] = "true"
		env.ExpectUpdated(node)

		node = common.ReplaceNodeConditions(node, unhealthyCondition)
		env.ExpectStatusUpdated(node)

		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, numPods)
	})
	It("should ignore terminationGracePeriod on the nodepool", func() {
		nodePool.Spec.Template.Spec.TerminationGracePeriod = &metav1.Duration{Duration: time.Hour}
		env.ExpectCreated(nodeClass, nodePool, dep)
		pod := env.EventuallyExpectHealthyPodCount(selector, numPods)[0]
		node := env.ExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)

		node = common.ReplaceNodeConditions(node, unhealthyCondition)
		env.ExpectStatusUpdated(node)

		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, numPods)
	})
})

var _ = Describe("Spot Interruption", func() {
	It("should leave an advisory running and then handle Preempt while the condition stays True", func() {
		dep := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"app": "spot-interruption"},
					Annotations: map[string]string{karpenterv1.DoNotDisruptAnnotationKey: "true"},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](180),
			},
		})
		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		env.ExpectCreated(nodeClass, nodePool, dep)
		pod := env.EventuallyExpectHealthyPodCount(selector, 1)[0]
		node := env.ExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)
		claims := &karpenterv1.NodeClaimList{}
		Expect(env.Client.List(env.Context, claims, client.MatchingLabels{karpenterv1.NodePoolLabelKey: nodePool.Name})).To(Succeed())
		Expect(claims.Items).To(HaveLen(1))
		claim := claims.Items[0].DeepCopy()

		condition := corev1.NodeCondition{
			Type: "PreemptionScheduled", Status: corev1.ConditionTrue, Reason: "SpotEvictionIncoming",
			Message:            fmt.Sprintf("SpotRebalanceRecommendation Advisory: %s.", time.Now().UTC().Format(time.RFC1123)),
			LastTransitionTime: metav1.Now(),
		}
		node = common.ReplaceNodeConditions(node, condition)
		env.ExpectStatusUpdated(node)
		Consistently(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(node), node)).To(Succeed())
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(claim), claim)).To(Succeed())
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
			g.Expect(node.DeletionTimestamp.IsZero()).To(BeTrue())
			g.Expect(node.Spec.Unschedulable).To(BeFalse())
			g.Expect(node.Spec.Taints).ToNot(ContainElement(karpenterv1.DisruptedNoScheduleTaint))
			g.Expect(claim.DeletionTimestamp.IsZero()).To(BeTrue())
			g.Expect(claim.Annotations).ToNot(HaveKey(karpenterv1.NodeClaimTerminationTimestampAnnotationKey))
			g.Expect(pod.DeletionTimestamp.IsZero()).To(BeTrue())
		}, 15*time.Second, time.Second).Should(Succeed())

		deadline := time.Now().UTC().Add(30 * time.Second).Truncate(time.Second)
		condition.Message = fmt.Sprintf("Preempt Scheduled: %s.", deadline.Format(time.RFC1123))
		node = common.ReplaceNodeConditions(node, condition)
		env.ExpectStatusUpdated(node)
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(claim), claim)).To(Succeed())
			g.Expect(claim.Annotations).To(HaveKeyWithValue(karpenterv1.NodeClaimTerminationTimestampAnnotationKey, deadline.Format(time.RFC3339)))
			g.Expect(claim.DeletionTimestamp.IsZero()).To(BeFalse())
		}, 20*time.Second, time.Second).Should(Succeed())
		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})
})
