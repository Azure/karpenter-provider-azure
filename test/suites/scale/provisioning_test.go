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

package scale_test

import (
	"context"
	"strconv"
	"time"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/debug"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
)

const testGroup = "provisioning"

var _ = Describe("Provisioning", Label(debug.NoWatch), Label(debug.NoEvents), func() {
	var nodePool *karpv1.NodePool
	var nodeClass *v1alpha2.AKSNodeClass
	var deployment *appsv1.Deployment
	var selector labels.Selector
	var dsCount int

	BeforeEach(func() {
		nodeClass = env.DefaultAKSNodeClass()
		nodePool = env.DefaultNodePool(nodeClass)
		nodePool.Spec.Limits = nil
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{
			Nodes: "70%",
		}}
		deployment = test.Deployment(test.DeploymentOptions{
			PodOptions: test.PodOptions{
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("50Mi"),
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		})
		selector = labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		// Get the DS pod count and use it to calculate the DS pod overhead
		dsCount = env.GetDaemonSetCount(nodePool)
	})
	It("should scale successfully on a node-dense scale-up", Label(debug.NoEvents), func(_ context.Context) {
		replicasPerNode := 1
		expectedNodeCount := 200
		replicas := replicasPerNode * expectedNodeCount

		deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
		// Hostname anti-affinity to require one pod on each node
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: deployment.Spec.Selector,
						TopologyKey:   corev1.LabelHostname,
					},
				},
			},
		}

		By("waiting for the deployment to deploy all of its pods")
		env.ExpectCreated(deployment)
		env.EventuallyExpectPendingPodCount(selector, replicas)

		env.MeasureProvisioningDurationFor(func() {
			By("kicking off provisioning by applying the nodePool and nodeClass")
			env.ExpectCreated(nodePool, nodeClass)

			env.EventuallyExpectCreatedNodeClaimCount("==", expectedNodeCount)
			env.EventuallyExpectCreatedNodeCount("==", expectedNodeCount)
			env.EventuallyExpectInitializedNodeCount("==", expectedNodeCount)
			env.EventuallyExpectHealthyPodCount(selector, replicas)
		}, map[string]string{
			azure.TestCategoryDimension:           testGroup,
			azure.TestNameDimension:               "node-dense",
			azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
			azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
			azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
		})
	}, SpecTimeout(time.Minute*30))
	It("should scale successfully on a pod-dense scale-up", func(_ context.Context) {
		replicasPerNode := 110
		maxPodDensity := replicasPerNode + dsCount
		expectedNodeCount := 60
		replicas := replicasPerNode * expectedNodeCount
		deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
		nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))
		///???
		test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
			// AWS: With Prefix Delegation enabled, .large instances can have 434 pods.
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1alpha2.LabelSKUCPU,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{"2"},
			},
		},
		)

		env.MeasureProvisioningDurationFor(func() {
			By("waiting for the deployment to deploy all of its pods")
			env.ExpectCreated(deployment)
			env.EventuallyExpectPendingPodCount(selector, replicas)

			By("kicking off provisioning by applying the nodePool and nodeClass")
			env.ExpectCreated(nodePool, nodeClass)

			env.EventuallyExpectHealthyPodCount(selector, replicas)
		}, map[string]string{
			azure.TestCategoryDimension:           testGroup,
			azure.TestNameDimension:               "pod-dense",
			azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
			azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
			azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
		})
	}, SpecTimeout(time.Minute*30))
})
