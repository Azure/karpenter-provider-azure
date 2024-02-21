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

package integration_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"

	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("Expiration", func() {

	var _ = BeforeEach(func() {
		nodePool.Spec.Disruption.ExpireAfter = corev1beta1.NillableDuration{Duration: lo.ToPtr(time.Second * 30)}
	})

	It("should expire the node after the expiration is reached", func() {
		var numPods int32 = 1
		dep := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: numPods,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						corev1beta1.DoNotDisruptAnnotationKey: "true",
					},
					Labels: map[string]string{"app": "large-app"},
				},
			},
		})
		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		env.ExpectCreated(nodeClass, nodePool, dep)

		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, int(numPods))
		env.Monitor.Reset() // Reset the monitor so that we can expect a single node to be spun up after expiration

		// Expect that the NodeClaim will get an expired status condition
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env, client.ObjectKeyFromObject(nodeClaim), nodeClaim)).To(Succeed())
			g.Expect(nodeClaim.StatusConditions().GetCondition(corev1beta1.Expired).IsTrue()).To(BeTrue())
		}).Should(Succeed())

		// Remove the do-not-disrupt annotation so that the Nodes are now deprovisionable
		for _, pod := range env.ExpectPodsMatchingSelector(selector) {
			delete(pod.Annotations, corev1beta1.DoNotDisruptAnnotationKey)
			env.ExpectUpdated(pod)
		}

		// Eventually the node will be set as unschedulable, which means its actively being deprovisioned
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(node), node)).Should(Succeed())
			_, ok := lo.Find(node.Spec.Taints, func(t v1.Taint) bool {
				return corev1beta1.IsDisruptingTaint(t)
			})
			g.Expect(ok).To(BeTrue())
		}).Should(Succeed())

		// Set the expireAfter to "Never" to make sure new node isn't deleted
		// This is CRITICAL since it prevents nodes that are immediately spun up from immediately being expired and
		// racing at the end of the E2E test, leaking node resources into subsequent tests
		nodePool.Spec.Disruption.ExpireAfter.Duration = nil
		env.ExpectUpdated(nodePool)

		// After the deletion timestamp is set and all pods are drained
		// the node should be gone
		env.EventuallyExpectNotFound(nodeClaim, node)

		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyPodCount(selector, int(numPods))
	})
	It("should replace expired node with a single node and schedule all pods", func() {
		var numPods int32 = 5
		// We should setup a PDB that will only allow a minimum of 1 pod to be pending at a time
		minAvailable := intstr.FromInt32(numPods - 1)
		pdb := coretest.PodDisruptionBudget(coretest.PDBOptions{
			Labels: map[string]string{
				"app": "large-app",
			},
			MinAvailable: &minAvailable,
		})
		dep := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: numPods,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						corev1beta1.DoNotDisruptAnnotationKey: "true",
					},
					Labels: map[string]string{"app": "large-app"},
				},
			},
		})
		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		env.ExpectCreated(nodeClass, nodePool, pdb, dep)

		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, int(numPods))
		env.Monitor.Reset() // Reset the monitor so that we can expect a single node to be spun up after expiration

		// Set the expireAfter value to get the node deleted
		nodePool.Spec.Disruption.ExpireAfter.Duration = lo.ToPtr(time.Minute)
		env.ExpectUpdated(nodePool)

		// Expect that the NodeClaim will get an expired status condition
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env, client.ObjectKeyFromObject(nodeClaim), nodeClaim)).To(Succeed())
			g.Expect(nodeClaim.StatusConditions().GetCondition(corev1beta1.Expired).IsTrue()).To(BeTrue())
		}).Should(Succeed())

		// Remove the do-not-disruption annotation so that the Nodes are now deprovisionable
		for _, pod := range env.ExpectPodsMatchingSelector(selector) {
			delete(pod.Annotations, corev1beta1.DoNotDisruptAnnotationKey)
			env.ExpectUpdated(pod)
		}

		// Eventually the node will be set as unschedulable, which means its actively being deprovisioned
		Eventually(func(g Gomega) {
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(node), node)).Should(Succeed())
			_, ok := lo.Find(node.Spec.Taints, func(t v1.Taint) bool {
				return corev1beta1.IsDisruptingTaint(t)
			})
			g.Expect(ok).To(BeTrue())
		}).Should(Succeed())

		// Set the expireAfter to "Never" to make sure new node isn't deleted
		// This is CRITICAL since it prevents nodes that are immediately spun up from immediately being expired and
		// racing at the end of the E2E test, leaking node resources into subsequent tests
		nodePool.Spec.Disruption.ExpireAfter.Duration = nil
		env.ExpectUpdated(nodePool)

		// After the deletion timestamp is set and all pods are drained
		// the node should be gone
		env.EventuallyExpectNotFound(nodeClaim, node)

		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		env.EventuallyExpectCreatedNodeCount("==", 1)
		env.EventuallyExpectHealthyPodCount(selector, int(numPods))
	})
})
