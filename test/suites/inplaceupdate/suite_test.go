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

package drift_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestInplaceUpdate(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Inplace Update")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Inplace Update", func() {
	var dep *appsv1.Deployment
	var selector labels.Selector

	Context("Tags", func() {
		It("should add tags in-place on all resources without drifting the nodeClaim", func() {
			Skip("While preforming Machine API Testing")
			var numPods int32 = 3
			appLabels := map[string]string{"app": "large-app"}
			dep = coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: appLabels,
					},
					// anti-affinity so that each pod is placed on a unique node
					PodAntiRequirements: []corev1.PodAffinityTerm{{
						TopologyKey: corev1.LabelHostname,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: appLabels,
						},
					}},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
				},
			})
			selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			env.ExpectCreated(nodeClass, nodePool, dep)

			nodeClaims := env.EventuallyExpectRegisteredNodeClaimCount("==", int(numPods))
			nodes := env.EventuallyExpectCreatedNodeCount("==", int(numPods))
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("adding tags to the nodeClass, which should propagate to the VMs")
			nodeClass.Spec.Tags = map[string]string{"tag1": "value1", "tag2": "value2"}
			expectedTags := lo.Assign(
				nodeClass.Spec.Tags,
				map[string]string{
					"karpenter.azure.com_cluster": env.ClusterName,
					"karpenter.sh_nodepool":       nodePool.Name,
				})

			env.ExpectUpdated(nodeClass)

			// Expect the nodeclaims to not be drifted, but the tags to be updated on the VMs
			env.EventuallyExpectTags(expectedTags)

			// Expect the nodeClaims and nodes to not be drifted
			env.ExpectAllExist(lo.Map(nodeClaims, func(obj *karpv1.NodeClaim, _ int) client.Object { return obj })...)
			env.ExpectAllExist(lo.Map(nodes, func(obj *corev1.Node, _ int) client.Object { return obj })...)
		})

		It("should remove tags in-place on all resources without drifting the nodeClaim", func() {
			Skip("While preforming Machine API Testing")
			nodeClass.Spec.Tags = map[string]string{"tag1": "value1", "tag2": "value2"}

			var numPods int32 = 3
			appLabels := map[string]string{"app": "large-app"}
			dep = coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: appLabels,
					},
					// anti-affinity so that each pod is placed on a unique node
					PodAntiRequirements: []corev1.PodAffinityTerm{{
						TopologyKey: corev1.LabelHostname,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: appLabels,
						},
					}},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
				},
			})
			selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			env.ExpectCreated(nodeClass, nodePool, dep)

			nodeClaims := env.EventuallyExpectRegisteredNodeClaimCount("==", int(numPods))
			nodes := env.EventuallyExpectCreatedNodeCount("==", int(numPods))
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("removing tags from the nodeClass, which should propagate to the VMs")
			expectedMissingTags := nodeClass.Spec.Tags
			nodeClass.Spec.Tags = nil // cleared entirely

			env.ExpectUpdated(nodeClass)

			// Expect the nodeclaims to not be drifted, but the tags to be updated on the VMs
			env.EventuallyExpectMissingTags(expectedMissingTags)

			// Expect the nodeClaims and nodes to not be drifted
			env.ExpectAllExist(lo.Map(nodeClaims, func(obj *karpv1.NodeClaim, _ int) client.Object { return obj })...)
			env.ExpectAllExist(lo.Map(nodes, func(obj *corev1.Node, _ int) client.Object { return obj })...)
		})

		It("should update tags in-place on all resources even when new nodes are being created", func() {
			Skip("While preforming Machine API Testing")
			var numPods int32 = 3
			appLabels := map[string]string{"app": "large-app"}
			dep = coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: appLabels,
					},
					// anti-affinity so that each pod is placed on a unique node
					PodAntiRequirements: []corev1.PodAffinityTerm{{
						TopologyKey: corev1.LabelHostname,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: appLabels,
						},
					}},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
				},
			})
			selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			env.ExpectCreated(nodeClass, nodePool, dep)

			nodeClaims := env.EventuallyExpectRegisteredNodeClaimCount("==", int(numPods))

			// Now update the NodeClass
			By("adding tags to the nodeClass, which should propagate to the VMs")
			nodeClass.Spec.Tags = map[string]string{"tag1": "value1", "tag2": "value2"}
			expectedTags := lo.Assign(
				nodeClass.Spec.Tags,
				map[string]string{
					"karpenter.azure.com_cluster": env.ClusterName,
					"karpenter.sh_nodepool":       nodePool.Name,
				})

			env.ExpectUpdated(nodeClass)
			env.EventuallyExpectCreatedNodeCount("==", int(numPods))
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			// Expect the nodeclaims to not be drifted, but the tags to be updated on the VMs
			env.EventuallyExpectTags(expectedTags)

			// Expect the nodeClaims to not be drifted
			env.ExpectAllExist(lo.Map(nodeClaims, func(obj *karpv1.NodeClaim, _ int) client.Object { return obj })...)
		})
	})
})
