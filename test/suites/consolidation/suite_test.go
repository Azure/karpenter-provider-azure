i/*
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
package consolidation

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	"github.com/aws/karpenter-core/pkg/test"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/karpenter/test/pkg/debug"
	environmentazure "github.com/Azure/karpenter/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentazure.Environment

func TestConsolidation(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = environmentazure.NewEnvironment(t)
	})
	RunSpecs(t, "Consolidation")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Consolidation", func() {
	It("should consolidate nodes (delete)", Label(debug.NoWatch), Label(debug.NoEvents), func() {
		provider := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(provider)
		nodePool.Spec.Disruption.ConsolidationPolicy = corev1beta1.ConsolidationPolicyWhenUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = &corev1beta1.NillableDuration{}

		var numPods int32 = 75
		dep := test.Deployment(test.DeploymentOptions{
			Replicas: numPods,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "large-app"},
				},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1")},
				},
			},
		})

		selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		env.ExpectCreatedNodeCount("==", 0)
		env.ExpectCreated(nodePool, provider, dep)

		env.EventuallyExpectHealthyPodCount(selector, int(numPods))

		// reduce the number of pods by 60%
		dep.Spec.Replicas = to.Ptr[int32](30)
		env.ExpectUpdated(dep)
		env.EventuallyExpectAvgUtilization(v1.ResourceCPU, "<", 0.5)

		nodePool.Spec.Disruption.ConsolidateAfter = nil
		env.ExpectUpdated(nodePool)

		// With consolidation enabled, we now must delete nodes
		env.EventuallyExpectAvgUtilization(v1.ResourceCPU, ">", 0.6)

		env.ExpectDeleted(dep)
	})
	It("should consolidate on-demand nodes (replace)", func() {
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)
		nodePool.Spec.Disruption.ConsolidationPolicy = corev1beta1.ConsolidationPolicyWhenUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = &corev1beta1.NillableDuration{}

		var numPods int32 = 3
		largeDep := test.Deployment(test.DeploymentOptions{
			Replicas: numPods,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "large-app"},
				},
				TopologySpreadConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       v1.LabelHostname,
						WhenUnsatisfiable: v1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "large-app",
							},
						},
					},
				},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("8")},
				},
			},
		})
		smallDep := test.Deployment(test.DeploymentOptions{
			Replicas: numPods,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "small-app"},
				},
				TopologySpreadConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       v1.LabelHostname,
						WhenUnsatisfiable: v1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "small-app",
							},
						},
					},
				},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1.5")},
				},
			},
		})
		selector := labels.SelectorFromSet(largeDep.Spec.Selector.MatchLabels)
		env.ExpectCreatedNodeCount("==", 0)
		env.ExpectCreated(nodeClass, nodePool, largeDep, smallDep)

		env.EventuallyExpectHealthyPodCount(selector, int(numPods))

		// 3 nodes due to the anti-affinity rules
		env.ExpectCreatedNodeCount("==", 3)

		// scaling down the large deployment leaves only small pods on each node
		largeDep.Spec.Replicas = to.Ptr[int32](0)
		env.ExpectUpdated(largeDep)
		env.EventuallyExpectAvgUtilization(v1.ResourceCPU, "<", 0.5)

		nodePool.Spec.Disruption.ConsolidateAfter = nil
		env.ExpectUpdated(nodePool)

		// With consolidation enabled, we now must replace each node in turn to consolidate due to the anti-affinity
		// rules on the smaller deployment.
		env.EventuallyExpectAvgUtilization(v1.ResourceCPU, ">", 0.8)
		env.ExpectDeleted(largeDep, smallDep)
	})
})
