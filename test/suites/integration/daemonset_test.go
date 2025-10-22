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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/client"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
)

var _ = Describe("DaemonSet", func() {
	var limitrange *corev1.LimitRange
	var priorityclass *schedulingv1.PriorityClass
	var daemonset *appsv1.DaemonSet
	var dep *appsv1.Deployment

	BeforeEach(func() {
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{"testing/cluster": "test"})

		priorityclass = &schedulingv1.PriorityClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "high-priority-daemonsets",
			},
			Value:         int32(10000000),
			GlobalDefault: false,
			Description:   "This priority class should be used for daemonsets.",
		}
		limitrange = &corev1.LimitRange{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "limitrange",
				Namespace: "default",
			},
		}
		daemonset = test.DaemonSet(test.DaemonSetOptions{
			PodOptions: test.PodOptions{
				ResourceRequirements: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}},
				PriorityClassName:    "high-priority-daemonsets",
			},
		})
		numPods := 1
		dep = test.Deployment(test.DeploymentOptions{
			Replicas: int32(numPods),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "large-app"},
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4")},
				},
			},
		})
	})
	It("should account for LimitRange Default on daemonSet pods for resources", func() {
		limitrange.Spec.Limits = []corev1.LimitRangeItem{
			{
				Type: corev1.LimitTypeContainer,
				Default: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		}

		podSelector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		daemonSetSelector := labels.SelectorFromSet(daemonset.Spec.Selector.MatchLabels)
		env.ExpectCreated(nodeClass, nodePool, limitrange, priorityclass, daemonset, dep)

		// Eventually expect a single node to exist and both the deployment pod and the daemonset pod to schedule to it
		Eventually(func(g Gomega) {
			nodeList := &corev1.NodeList{}
			g.Expect(env.Client.List(env, nodeList, client.HasLabels{"testing/cluster"})).To(Succeed())
			g.Expect(nodeList.Items).To(HaveLen(1))

			deploymentPods := env.Monitor.RunningPods(podSelector)
			g.Expect(deploymentPods).To(HaveLen(1))

			daemonSetPods := env.Monitor.RunningPods(daemonSetSelector)
			g.Expect(daemonSetPods).To(HaveLen(1))

			g.Expect(deploymentPods[0].Spec.NodeName).To(Equal(nodeList.Items[0].Name))
			g.Expect(daemonSetPods[0].Spec.NodeName).To(Equal(nodeList.Items[0].Name))
		}).Should(Succeed())
	})
	It("should account for LimitRange DefaultRequest on daemonSet pods for resources", func() {
		limitrange.Spec.Limits = []corev1.LimitRangeItem{
			{
				Type: corev1.LimitTypeContainer,
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		}

		podSelector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		daemonSetSelector := labels.SelectorFromSet(daemonset.Spec.Selector.MatchLabels)
		env.ExpectCreated(nodeClass, nodePool, limitrange, priorityclass, daemonset, dep)

		// Eventually expect a single node to exist and both the deployment pod and the daemonset pod to schedule to it
		Eventually(func(g Gomega) {
			nodeList := &corev1.NodeList{}
			g.Expect(env.Client.List(env, nodeList, client.HasLabels{"testing/cluster"})).To(Succeed())
			g.Expect(nodeList.Items).To(HaveLen(1))

			deploymentPods := env.Monitor.RunningPods(podSelector)
			g.Expect(deploymentPods).To(HaveLen(1))

			daemonSetPods := env.Monitor.RunningPods(daemonSetSelector)
			g.Expect(daemonSetPods).To(HaveLen(1))

			g.Expect(deploymentPods[0].Spec.NodeName).To(Equal(nodeList.Items[0].Name))
			g.Expect(daemonSetPods[0].Spec.NodeName).To(Equal(nodeList.Items[0].Name))
		}).Should(Succeed())
	})
	It("should schedule DaemonSet with matching AKS domain node affinity (small/medium/large)", func() {
		// DaemonSet 1: Small (CPU <= 2).
		daemonsetSmall := test.DaemonSet(test.DaemonSetOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "ds-small"},
			PodOptions: test.PodOptions{
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{Key: v1beta1.AKSLabelCPU, Operator: corev1.NodeSelectorOpLt, Values: []string{"3"}},
				},
			},
		})

		// DaemonSet 2: Medium (CPU 3-4).
		daemonsetMedium := test.DaemonSet(test.DaemonSetOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "ds-medium"},
			PodOptions: test.PodOptions{
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{Key: v1beta1.AKSLabelCPU, Operator: corev1.NodeSelectorOpGt, Values: []string{"2"}},
					{Key: v1beta1.AKSLabelCPU, Operator: corev1.NodeSelectorOpLt, Values: []string{"5"}},
				},
			},
		})

		// DaemonSet 3: Large (CPU 5+).
		daemonsetLarge := test.DaemonSet(test.DaemonSetOptions{
			ObjectMeta: metav1.ObjectMeta{Name: "ds-large"},
			PodOptions: test.PodOptions{
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{Key: v1beta1.AKSLabelCPU, Operator: corev1.NodeSelectorOpGt, Values: []string{"4"}},
				},
			},
		})

		// Deployment targeting 2 CPU core.
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				NodeSelector: map[string]string{
					v1beta1.AKSLabelCPU: "2",
				},
			},
		})

		env.ExpectCreated(nodeClass, nodePool, daemonsetSmall, daemonsetMedium, daemonsetLarge, deployment)

		// Eventually expect deployment and small DaemonSet to schedule.
		Eventually(func(g Gomega) {
			nodeList := &corev1.NodeList{}
			g.Expect(env.Client.List(env, nodeList, client.HasLabels{"testing/cluster"})).To(Succeed())
			g.Expect(nodeList.Items).To(HaveLen(1))

			node := nodeList.Items[0]
			g.Expect(node.Labels).To(HaveKeyWithValue(v1beta1.AKSLabelCPU, "2"))

			deploymentPods := env.Monitor.RunningPods(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels))
			g.Expect(deploymentPods).To(HaveLen(1))
			g.Expect(deploymentPods[0].Spec.NodeName).To(Equal(node.Name))

			smallDSPods := env.Monitor.RunningPods(labels.SelectorFromSet(daemonsetSmall.Spec.Selector.MatchLabels))
			g.Expect(smallDSPods).To(HaveLen(1))
			g.Expect(smallDSPods[0].Spec.NodeName).To(Equal(node.Name))

			mediumDSPods := env.Monitor.RunningPods(labels.SelectorFromSet(daemonsetMedium.Spec.Selector.MatchLabels))
			g.Expect(mediumDSPods).To(HaveLen(0))
			largeDSPods := env.Monitor.RunningPods(labels.SelectorFromSet(daemonsetLarge.Spec.Selector.MatchLabels))
			g.Expect(largeDSPods).To(HaveLen(0))
		}).Should(Succeed())
	})
})
