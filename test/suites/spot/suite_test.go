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

package spot_test

import (
	"testing"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestSpot(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Spot")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Spot", func() {
	BeforeEach(func() {
		// Create a node pool with spot requirement
		nodePool = test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeSpot},
			}})
		env.ExpectCreated(nodePool, nodeClass)
	})

	It("should provision replacement nodes after spot evictions", func() {
		podLabels := map[string]string{"app": "spot-test"}
		dep := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				NodeSelector: map[string]string{
					karpv1.CapacityTypeLabelKey: karpv1.CapacityTypeSpot,
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "kubernetes.azure.com/scalesetpriority",
						Operator: corev1.TolerationOpEqual,
						Value:    "spot",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
			},
		})

		// Create resources
		env.ExpectCreated(dep)

		// Verify pods are scheduled and running
		pods := env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 1)

		// Verify nodes are created with the spot capacity type label
		nodes := env.ExpectCreatedNodeCount("==", 1)
		Expect(nodes[0].Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeSpot))
		Expect(nodes[0].Labels).To(HaveKeyWithValue(v1beta1.AKSLabelScaleSetPriority, v1beta1.ScaleSetPrioritySpot))

		// Simulate spot eviction
		env.SimulateVMEviction(nodes[0].Name)

		// Verify that a node is deleted
		env.EventuallyExpectNotFound(nodes[0])
		env.EventuallyExpectNotFound(pods[0])

		// Verify pods are scheduled and running after replacement
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 1)

		// Cleanup resources
		env.ExpectDeleted(dep)
	})
})

var _ = Describe("Spot (nonstandard node pool)", func() {
	It("should provision spot nodes via kubernetes.azure.com/scalesetpriority label", func() {
		// Create a node pool with spot requirement
		nodePool = test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1beta1.AKSLabelScaleSetPriority,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{v1beta1.ScaleSetPrioritySpot},
			}})
		nodePool.Spec.Template.Spec.Requirements = lo.Reject(nodePool.Spec.Template.Spec.Requirements, func(r karpv1.NodeSelectorRequirementWithMinValues, _ int) bool {
			return r.Key == karpv1.CapacityTypeLabelKey
		})
		env.ExpectCreated(nodePool, nodeClass)

		podLabels := map[string]string{"app": "spot-test"}
		dep := test.Deployment(test.DeploymentOptions{
			Replicas: 1,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				NodeSelector: map[string]string{
					v1beta1.AKSLabelScaleSetPriority: v1beta1.ScaleSetPrioritySpot,
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "kubernetes.azure.com/scalesetpriority",
						Operator: corev1.TolerationOpEqual,
						Value:    "spot",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr(int64(0)),
			},
		})

		// Create resources
		env.ExpectCreated(dep)

		// Verify pods are scheduled and running
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), 1)

		// Verify nodes are created with the spot capacity type label
		nodes := env.ExpectCreatedNodeCount("==", 1)
		Expect(nodes[0].Labels).To(HaveKeyWithValue(karpv1.CapacityTypeLabelKey, karpv1.CapacityTypeSpot))
		Expect(nodes[0].Labels).To(HaveKeyWithValue(v1beta1.AKSLabelScaleSetPriority, v1beta1.ScaleSetPrioritySpot))

		// Cleanup resources
		env.ExpectDeleted(dep)
	})
})
