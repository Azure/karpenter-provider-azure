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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"
)

// Helper function to find a requirement by key from the list of requirements
func findNodeSelectorRequirementByKey(requirements []karpv1.NodeSelectorRequirementWithMinValues, key string) *corev1.NodeSelectorRequirement {
	for _, req := range requirements {
		if req.Key == key {
			return &corev1.NodeSelectorRequirement{Key: req.Key, Operator: req.Operator, Values: req.Values}
		}
	}
	return nil
}

var _ = Describe("Zone Constraints", func() {
	BeforeEach(func() {
		// Create a node pool with zone constraints
		nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{env.Region + "-2"},
		})
		env.ExpectCreated(nodePool, nodeClass)
	})

	It("should be respected", func() {
		// Deploy spread pods
		podCount := 6
		podLabels := map[string]string{"test": "spread"}
		spreadDep := test.Deployment(test.DeploymentOptions{
			Replicas: int32(podCount),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				PodAntiRequirements: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: podLabels,
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		})
		env.ExpectCreated(spreadDep)

		// Verify that all pods are created
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(spreadDep.Spec.Selector.MatchLabels), podCount)

		// Verify nodes are created with the count equals to replicas, all in the correct zone
		nodes := env.ExpectCreatedNodeCount("==", podCount)
		for _, node := range nodes {
			// Find the zone requirement from nodePool requirements
			zoneConstraint := findNodeSelectorRequirementByKey(nodePool.Spec.Template.Spec.Requirements, corev1.LabelTopologyZone)
			Expect(zoneConstraint).NotTo(BeNil(), "Zone requirement not found in nodePool")

			// Check that node zone is one of the allowed values
			nodeZone := node.Labels[corev1.LabelTopologyZone]
			Expect(zoneConstraint.Values).To(ContainElement(nodeZone),
				"Node zone %s is not in the allowed zones %v", nodeZone, zoneConstraint.Values)
		}

		// Delete the deployment to clean up
		env.ExpectDeleted(spreadDep)
	})

	It("should create nodes that zone constraints can satisfy", func() {
		zoneConstraint := findNodeSelectorRequirementByKey(nodePool.Spec.Template.Spec.Requirements, corev1.LabelTopologyZone)
		Expect(zoneConstraint).NotTo(BeNil(), "Zone requirement not found in nodePool")
		allowedZonesCount := len(zoneConstraint.Values)

		// Deploy zone spread pods, more than the allowed zones
		podCount := allowedZonesCount * 2
		podLabels := map[string]string{"test": "zonal-spread"}
		zoneSpreadDep := test.Deployment(test.DeploymentOptions{
			Replicas: int32(podCount),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       corev1.LabelTopologyZone,
						WhenUnsatisfiable: corev1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: podLabels,
						},
					},
				},
			},
		})
		env.ExpectCreated(zoneSpreadDep)

		// Verify that pod is created as much as possible, == number of allowed zones
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(zoneSpreadDep.Spec.Selector.MatchLabels), allowedZonesCount)

		// Verify node count consistently stays, only counting nodes created by this test
		env.ConsistentlyExpectCreatedNodeCount("==", allowedZonesCount, 5*time.Minute)

		// Verify that pod count is still the same
		env.ExpectHealthyPodCount(labels.SelectorFromSet(zoneSpreadDep.Spec.Selector.MatchLabels), allowedZonesCount)

		// Delete the deployment to clean up
		env.ExpectDeleted(zoneSpreadDep)
	})

	It("should not create nodes if zone constraints cannot satisfy", func() {
		// Deploy pods that nodepool constraints cannot be satisfied
		podLabels := map[string]string{"test": "unsatisfiable"}
		unsatisfiableDep := test.Deployment(test.DeploymentOptions{
			Replicas: int32(3),
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				NodeRequirements: []corev1.NodeSelectorRequirement{
					{
						Key:      corev1.LabelTopologyZone,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{env.Region + "-1"},
					},
				},
			},
		})
		env.ExpectCreated(unsatisfiableDep)

		// Verify that no nodes are created as a result of the unsatisfiable deployment
		env.ConsistentlyExpectNodeCount("==", 0, 5*time.Minute)

		// Delete the deployment to clean up
		env.ExpectDeleted(unsatisfiableDep)
	})
})
