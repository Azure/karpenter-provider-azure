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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("Zone Spread", func() {
	It("should spread nodes across zones without any zone constraints", func() {
		if !env.SupportsZones() {
			Skip(fmt.Sprintf("skipping zone spread test because region %s does not support availability zones", env.Region))
		}
		availableZones := env.GetAvailableZones()
		if len(availableZones) < 2 {
			Skip(fmt.Sprintf("skipping zone spread test because region %s has fewer than two availability zones", env.Region))
		}

		env.ExpectCreated(nodePool, nodeClass)

		// Anti-affinity on hostname gives one node per pod. The pods carry no zone selector and no
		// topology spread constraints, so the zone of each node is chosen entirely by the provider.
		podCount := 2 * len(availableZones)
		podLabels := map[string]string{"test": "zone-spread"}
		dep := test.Deployment(test.DeploymentOptions{
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
						TopologyKey: corev1.LabelHostname,
					},
				},
			},
		})
		env.ExpectCreated(dep)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), podCount)

		nodes := env.ExpectCreatedNodeCount("==", podCount)
		zoneCounts := map[string]int{}
		for _, node := range nodes {
			zoneCounts[node.Labels[corev1.LabelTopologyZone]]++
		}

		// Zone selection prefers the NodePool's least loaded zone, so the nodes should come out
		// balanced rather than concentrated. The skew check is the meaningful assertion; the zone
		// count guards against a regression that pins every launch to a single zone.
		Expect(len(zoneCounts)).To(BeNumerically(">=", 2), "expected nodes in more than one zone, got %v", zoneCounts)
		minCount, maxCount := podCount, 0
		for _, count := range zoneCounts {
			minCount = min(minCount, count)
			maxCount = max(maxCount, count)
		}
		Expect(maxCount-minCount).To(BeNumerically("<=", 1), "expected nodes to be evenly spread across zones, got %v", zoneCounts)

		env.ExpectDeleted(dep)
	})
})
