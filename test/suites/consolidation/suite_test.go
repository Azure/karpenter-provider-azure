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

package consolidation_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/debug"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/common"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *azure.Environment

func TestConsolidation(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Consolidation")
}

var nodeClass *v1beta1.AKSNodeClass

var _ = BeforeEach(func() {
	nodeClass = env.DefaultAKSNodeClass()
	env.BeforeEach()
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Consolidation", Ordered, func() {
	Context("LastPodEventTime", func() {
		var nodePool *karpv1.NodePool
		BeforeEach(func() {
			nodePool = env.DefaultNodePool(nodeClass)
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")
		})
		It("should update lastPodEventTime when pods are scheduled and removed", func() {
			var numPods int32 = 5
			dep := coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "regular-app"},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
					},
				},
			})
			selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{
				{
					Nodes: "0%",
				},
			}
			env.ExpectCreated(nodeClass, nodePool, dep)

			nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)
			env.EventuallyExpectCreatedNodeCount("==", 1)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			nodeClaim := env.ExpectExists(nodeClaims[0]).(*karpv1.NodeClaim)
			lastPodEventTime := nodeClaim.Status.LastPodEventTime

			// wait 10 seconds so that we don't run into the de-dupe timeout
			// https://github.com/kubernetes-sigs/karpenter/blob/9daeda1ffdcead28c99d148d5d2a7ccecd9ad58f/pkg/controllers/nodeclaim/podevents/controller.go#L41-L44
			time.Sleep(10 * time.Second)

			dep.Spec.Replicas = lo.ToPtr[int32](4)
			By("removing one pod from the node")
			env.ExpectUpdated(dep)

			Eventually(func(g Gomega) {
				nodeClaim = env.ExpectExists(nodeClaim).(*karpv1.NodeClaim)
				g.Expect(nodeClaim.Status.LastPodEventTime.Time).ToNot(BeEquivalentTo(lastPodEventTime.Time))
			}).WithTimeout(5 * time.Second).WithPolling(1 * time.Second).Should(Succeed())
			lastPodEventTime = nodeClaim.Status.LastPodEventTime

			// wait 10 seconds so that we don't run into the de-dupe timeout
			time.Sleep(10 * time.Second)

			dep.Spec.Replicas = lo.ToPtr[int32](5)
			By("adding one pod to the node")
			env.ExpectUpdated(dep)

			Eventually(func(g Gomega) {
				nodeClaim = env.ExpectExists(nodeClaim).(*karpv1.NodeClaim)
				g.Expect(nodeClaim.Status.LastPodEventTime.Time).ToNot(BeEquivalentTo(lastPodEventTime.Time))
			}).WithTimeout(5 * time.Second).WithPolling(1 * time.Second).Should(Succeed())
		})
		It("should update lastPodEventTime when pods go terminal", func() {
			podLabels := map[string]string{"app": "regular-app"}
			pod := coretest.Pod(coretest.PodOptions{
				// use a non-pause image so that we can have a sleep
				Image:   "alpine:3.20.2",
				Command: []string{"/bin/sh", "-c", "sleep 30"},
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
				},
				RestartPolicy: corev1.RestartPolicyNever,
			})
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      coretest.RandomName(),
					Namespace: "default",
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: pod.ObjectMeta,
						Spec:       pod.Spec,
					},
				},
			}
			selector := labels.SelectorFromSet(podLabels)
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{
				{
					Nodes: "0%",
				},
			}
			env.ExpectCreated(nodeClass, nodePool, job)

			nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)
			env.EventuallyExpectCreatedNodeCount("==", 1)
			pods := env.EventuallyExpectHealthyPodCount(selector, int(1))

			// pods are healthy, which means the job has started its 30s sleep
			nodeClaim := env.ExpectExists(nodeClaims[0]).(*karpv1.NodeClaim)
			lastPodEventTime := nodeClaim.Status.LastPodEventTime

			// wait a minute for the pod's sleep to finish, and for the nodeclaim to update
			Eventually(func(g Gomega) {
				pod := env.ExpectExists(pods[0]).(*corev1.Pod)
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodSucceeded))
			}).WithTimeout(1 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			nodeClaim = env.ExpectExists(nodeClaims[0]).(*karpv1.NodeClaim)
			Expect(nodeClaim.Status.LastPodEventTime).ToNot(BeEquivalentTo(lastPodEventTime.Time))
		})

	})
	Context("Budgets", func() {
		var nodePool *karpv1.NodePool
		var dep *appsv1.Deployment
		var selector labels.Selector
		var numPods int32
		BeforeEach(func() {
			nodePool = env.DefaultNodePool(nodeClass)
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

			numPods = 5
			dep = coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "regular-app"},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
					},
				},
			})
			selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
		})
		It("should respect budgets for empty delete consolidation", func() {
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{
				{
					Nodes: "40%",
				},
			}

			// Hostname anti-affinity to require one pod on each node
			dep.Spec.Template.Spec.Affinity = &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: dep.Spec.Selector,
							TopologyKey:   corev1.LabelHostname,
						},
					},
				},
			}
			env.ExpectCreated(nodeClass, nodePool, dep)

			env.EventuallyExpectCreatedNodeClaimCount("==", 5)
			nodes := env.EventuallyExpectCreatedNodeCount("==", 5)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("adding finalizers to the nodes to prevent termination")
			for _, node := range nodes {
				Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(node), node)).To(Succeed())
				node.Finalizers = append(node.Finalizers, common.TestingFinalizer)
				env.ExpectUpdated(node)
			}

			dep.Spec.Replicas = lo.ToPtr[int32](1)
			By("making the nodes empty")
			// Update the deployment to only contain 1 replica.
			env.ExpectUpdated(dep)

			env.ConsistentlyExpectDisruptionsUntilNoneLeft(5, 2, 10*time.Minute)
		})
		It("should respect budgets for non-empty delete consolidation", func() {
			// This test will hold consolidation until we are ready to execute it
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")

			nodePool = coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUCPU,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"8"},
					},
				},
			)
			// We're expecting to create 3 nodes, so we'll expect to see at most 2 nodes deleting at one time.
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{
				Nodes: "50%",
			}}
			numPods = 9
			dep = coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "large-app"},
					},
					// with 8 cpus, each node should fit no more than 3 pods.
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2100m"),
						},
					},
				},
			})
			selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			env.ExpectCreated(nodeClass, nodePool, dep)

			env.EventuallyExpectCreatedNodeClaimCount("==", 3)
			nodes := env.EventuallyExpectCreatedNodeCount("==", 3)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("scaling down the deployment")
			// Update the deployment to a third of the replicas.
			dep.Spec.Replicas = lo.ToPtr[int32](3)
			env.ExpectUpdated(dep)

			env.ForcePodsToSpread(nodes...)
			env.EventuallyExpectHealthyPodCount(selector, 3)

			By("cordoning and adding finalizer to the nodes")
			// Add a finalizer to each node so that we can stop termination disruptions
			for _, node := range nodes {
				Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(node), node)).To(Succeed())
				node.Finalizers = append(node.Finalizers, common.TestingFinalizer)
				env.ExpectUpdated(node)
			}

			By("enabling consolidation")
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
			env.ExpectUpdated(nodePool)

			env.ConsistentlyExpectDisruptionsUntilNoneLeft(3, 2, 10*time.Minute)
		})
		It("should respect budgets for non-empty replace consolidation", func() {
			appLabels := map[string]string{"app": "large-app"}
			// This test will hold consolidation until we are ready to execute it
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")

			nodePool = coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUCPU,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"4", "8"},
					},
				},
				// Add an Exists operator so that we can select on a fake partition later
				karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      "test-partition",
						Operator: corev1.NodeSelectorOpExists,
					},
				},
			)
			nodePool.Labels = appLabels
			// We're expecting to create 5 nodes, so we'll expect to see at most 3 nodes deleting at one time.
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{
				Nodes: "3",
			}}

			ds := coretest.DaemonSet(coretest.DaemonSetOptions{
				Selector: appLabels,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: appLabels,
					},
					// with 8 cpu, so each node should fit no more than 1 pod since each node will have
					// an equivalently sized daemonset
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
				},
			})

			env.ExpectCreated(ds)

			// Make 5 pods all with different deployments and different test partitions, so that each pod can be put
			// on a separate node.
			selector = labels.SelectorFromSet(appLabels)
			numPods = 5
			deployments := make([]*appsv1.Deployment, numPods)
			for i := range lo.Range(int(numPods)) {
				deployments[i] = coretest.Deployment(coretest.DeploymentOptions{
					Replicas: 1,
					PodOptions: coretest.PodOptions{
						ObjectMeta: metav1.ObjectMeta{
							Labels: appLabels,
						},
						NodeSelector: map[string]string{"test-partition": fmt.Sprintf("%d", i)},
						// with 8 cpu, each node should fit no more than 1 pod since each node will have
						// an equivalently sized daemonset
						ResourceRequirements: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("3"),
							},
						},
					},
				})
			}

			env.ExpectCreated(nodeClass, nodePool, deployments[0], deployments[1], deployments[2], deployments[3], deployments[4])

			originalNodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 5)
			originalNodes := env.EventuallyExpectCreatedNodeCount("==", 5)

			// Check that all daemonsets and deployment pods are online
			env.EventuallyExpectHealthyPodCount(selector, int(numPods)*2)

			By("cordoning and adding finalizer to the nodes")
			// Add a finalizer to each node so that we can stop termination disruptions
			for _, node := range originalNodes {
				Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(node), node)).To(Succeed())
				node.Finalizers = append(node.Finalizers, common.TestingFinalizer)
				env.ExpectUpdated(node)
			}

			// Delete the daemonset so that the nodes can be consolidated to smaller size
			env.ExpectDeleted(ds)
			// Check that all daemonsets and deployment pods are online
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("enabling consolidation")
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
			env.ExpectUpdated(nodePool)

			// Ensure that we get three nodes tainted, and they have overlap during the consolidation
			env.EventuallyExpectTaintedNodeCount("==", 3)
			env.EventuallyExpectLaunchedNodeClaimCount("==", 8)
			env.EventuallyExpectNodeCount("==", 8)

			env.ConsistentlyExpectDisruptionsUntilNoneLeft(5, 3, 10*time.Minute)

			for _, node := range originalNodes {
				Expect(env.ExpectTestingFinalizerRemoved(node)).To(Succeed())
			}
			for _, nodeClaim := range originalNodeClaims {
				Expect(env.ExpectTestingFinalizerRemoved(nodeClaim)).To(Succeed())
			}
			// Eventually expect all the nodes to be rolled and completely removed
			// Since this completes the disruption operation, this also ensures that we aren't leaking nodes into subsequent
			// tests since nodeclaims that are actively replacing but haven't brought-up nodes yet can register nodes later
			env.EventuallyExpectNotFound(lo.Map(originalNodes, func(n *corev1.Node, _ int) client.Object { return n })...)
			env.EventuallyExpectNotFound(lo.Map(originalNodeClaims, func(n *karpv1.NodeClaim, _ int) client.Object { return n })...)
			env.ExpectNodeClaimCount("==", 5)
			env.ExpectNodeCount("==", 5)
		})
		It("should not allow consolidation if the budget is fully blocking", func() {
			// We're going to define a budget that doesn't allow any consolidation to happen
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{
				Nodes: "0",
			}}

			// Hostname anti-affinity to require one pod on each node
			dep.Spec.Template.Spec.Affinity = &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: dep.Spec.Selector,
							TopologyKey:   corev1.LabelHostname,
						},
					},
				},
			}
			env.ExpectCreated(nodeClass, nodePool, dep)

			env.EventuallyExpectCreatedNodeClaimCount("==", 5)
			env.EventuallyExpectCreatedNodeCount("==", 5)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			dep.Spec.Replicas = lo.ToPtr[int32](1)
			By("making the nodes empty")
			// Update the deployment to only contain 1 replica.
			env.ExpectUpdated(dep)

			env.ConsistentlyExpectNoDisruptions(5, time.Minute)
		})
		It("should not allow consolidation if the budget is fully blocking during a scheduled time", func() {
			// We're going to define a budget that doesn't allow any drift to happen
			// This is going to be on a schedule that only lasts 30 minutes, whose window starts 15 minutes before
			// the current time and extends 15 minutes past the current time
			// Times need to be in UTC since the karpenter containers were built in UTC time
			windowStart := time.Now().Add(-time.Minute * 15).UTC()
			nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{
				Nodes:    "0",
				Schedule: lo.ToPtr(fmt.Sprintf("%d %d * * *", windowStart.Minute(), windowStart.Hour())),
				Duration: &metav1.Duration{Duration: time.Minute * 30},
			}}

			// Hostname anti-affinity to require one pod on each node
			dep.Spec.Template.Spec.Affinity = &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: dep.Spec.Selector,
							TopologyKey:   corev1.LabelHostname,
						},
					},
				},
			}
			env.ExpectCreated(nodeClass, nodePool, dep)

			env.EventuallyExpectCreatedNodeClaimCount("==", 5)
			env.EventuallyExpectCreatedNodeCount("==", 5)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			dep.Spec.Replicas = lo.ToPtr[int32](1)
			By("making the nodes empty")
			// Update the deployment to only contain 1 replica.
			env.ExpectUpdated(dep)

			env.ConsistentlyExpectNoDisruptions(5, time.Minute)
		})
	})
	DescribeTable("should consolidate nodes (delete)", Label(debug.NoWatch), Label(debug.NoEvents),
		func(spotToSpot bool) {
			nodePool := coretest.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Disruption: karpv1.Disruption{
						ConsolidationPolicy: karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized,
						// Disable Consolidation until we're ready
						ConsolidateAfter: karpv1.MustParseNillableDuration("Never"),
					},
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      karpv1.CapacityTypeLabelKey,
										Operator: corev1.NodeSelectorOpIn,
										Values:   lo.Ternary(spotToSpot, []string{karpv1.CapacityTypeSpot}, []string{karpv1.CapacityTypeOnDemand}),
									},
								},
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      v1beta1.LabelSKUCPU,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"2", "4", "8"},
									},
								},
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      v1beta1.LabelSKUFamily,
										Operator: corev1.NodeSelectorOpNotIn,
										// remove some cheap burstable types so we have more control over what gets provisioned
										Values: []string{"B"},
									},
								},
							},
							NodeClassRef: &karpv1.NodeClassReference{
								Group: object.GVK(nodeClass).Group,
								Kind:  object.GVK(nodeClass).Kind,
								Name:  nodeClass.Name,
							},
						},
					},
				},
			})
			nodePool = env.AdaptToClusterConfig(nodePool)

			var numPods int32 = 50
			dep := coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "large-app"},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
					},
				},
			})

			selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
			env.ExpectCreatedNodeCount("==", 0)
			env.ExpectCreated(nodePool, nodeClass, dep)

			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("reducing the number of pods by 60%")
			dep.Spec.Replicas = lo.ToPtr[int32](20)
			env.ExpectUpdated(dep)
			By("waiting for avg CPU utilization to drop < 0.5")
			env.EventuallyExpectAvgUtilization(corev1.ResourceCPU, "<", 0.5)

			// Enable consolidation as WhenEmptyOrUnderutilized doesn't allow a consolidateAfter value
			By("enabling consolidation")
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
			env.ExpectUpdated(nodePool)

			// With consolidation enabled, we now must delete nodes
			By("waiting for avg CPU utilization to increase > 0.6")
			env.EventuallyExpectAvgUtilization(corev1.ResourceCPU, ">", 0.6)

			By("deleting the deployment")
			env.ExpectDeleted(dep)
		},
		Entry("if the nodes are on-demand nodes", false),
		Entry("if the nodes are spot nodes", true),
	)
	DescribeTable("should consolidate nodes (replace)",
		func(spotToSpot bool) {

			if spotToSpot {
				if env.InClusterController {
					env.ExpectSettingsOverridden(corev1.EnvVar{Name: "FEATURE_GATES", Value: "SpotToSpotConsolidation=True"})
				} else {
					Skip("This test requires the controller to be running in-cluster (to ensure SpotToSpotConsolidation feature gate is enabled")
				}
			}

			nodePool := coretest.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Disruption: karpv1.Disruption{
						ConsolidationPolicy: karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized,
						// Disable Consolidation until we're ready
						ConsolidateAfter: karpv1.MustParseNillableDuration("Never"),
					},
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      karpv1.CapacityTypeLabelKey,
										Operator: corev1.NodeSelectorOpIn,
										Values:   lo.Ternary(spotToSpot, []string{karpv1.CapacityTypeSpot}, []string{karpv1.CapacityTypeOnDemand}),
									},
								},
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      v1beta1.LabelSKUCPU,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"2", "8"},
									},
								},
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      v1beta1.LabelSKUFamily,
										Operator: corev1.NodeSelectorOpNotIn,
										// remove some cheap burstable types so we have more control over what gets provisioned
										Values: []string{"B"},
									},
								},
							},
							NodeClassRef: &karpv1.NodeClassReference{
								Group: object.GVK(nodeClass).Group,
								Kind:  object.GVK(nodeClass).Kind,
								Name:  nodeClass.Name,
							},
						},
					},
				},
			})
			nodePool = env.AdaptToClusterConfig(nodePool)

			var numPods int32 = 3
			largeDep := coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "large-app"},
					},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
						{
							MaxSkew:           1,
							TopologyKey:       corev1.LabelHostname,
							WhenUnsatisfiable: corev1.DoNotSchedule,
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "large-app",
								},
							},
						},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
					},
				},
			})
			smallDep := coretest.Deployment(coretest.DeploymentOptions{
				Replicas: numPods,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "small-app"},
					},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
						{
							MaxSkew:           1,
							TopologyKey:       corev1.LabelHostname,
							WhenUnsatisfiable: corev1.DoNotSchedule,
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "small-app",
								},
							},
						},
					},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: func() resource.Quantity {
								dsOverhead := env.GetDaemonSetOverhead(nodePool)
								base := lo.ToPtr(resource.MustParse("1900m"))
								base.Sub(*dsOverhead.Cpu())
								return *base
							}(),
						},
					},
				},
			})

			By("creating a large and a small deployment")
			selector := labels.SelectorFromSet(largeDep.Spec.Selector.MatchLabels)
			env.ExpectCreatedNodeCount("==", 0)
			env.ExpectCreated(nodePool, nodeClass, largeDep, smallDep)

			By("checking that all pods are healthy")
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("waiting for 3 nodes (due to anti-affinity rules)")
			env.ExpectCreatedNodeCount("==", 3)

			By("scaling down the large deployment (leaving only small pods on each node)")
			largeDep.Spec.Replicas = lo.ToPtr[int32](0)
			env.ExpectUpdated(largeDep)

			By("waiting for avg utilization < 0.5")
			env.EventuallyExpectAvgUtilization(corev1.ResourceCPU, "<", 0.5)

			By("enabling consolidation")
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
			env.ExpectUpdated(nodePool)

			// With consolidation enabled, we now must replace each node in turn to consolidate due to the anti-affinity
			// rules on the smaller deployment. The 8cpu nodes should go to 2cpu nodes
			By("waiting for avg utilization > 0.7")
			env.EventuallyExpectAvgUtilization(corev1.ResourceCPU, ">", 0.7)

			var nodes corev1.NodeList
			Expect(env.Client.List(env.Context, &nodes)).To(Succeed())
			num2CpuNodes := 0
			numOtherNodes := 0
			for _, n := range nodes.Items {
				// only count the nodes created by the provisoiner
				if n.Labels[karpv1.NodePoolLabelKey] != nodePool.Name {
					continue
				}
				if n.Status.Capacity.Cpu().Cmp(resource.MustParse("2")) == 0 {
					num2CpuNodes++
				} else {
					numOtherNodes++
				}
			}

			By("checking that only smaller nodes are left")
			// all of the 8cpu nodes should have been replaced with 2cpu instance types
			Expect(num2CpuNodes).To(Equal(3))
			// and we should have no other nodes
			Expect(numOtherNodes).To(Equal(0))

			env.ExpectDeleted(largeDep, smallDep)
		},
		Entry("if the nodes are on-demand nodes", false),
		Entry("if the nodes are spot nodes", true),
	)
	It("should consolidate on-demand nodes to spot (replace)", func() {
		nodePool := coretest.NodePool(karpv1.NodePool{
			Spec: karpv1.NodePoolSpec{
				Disruption: karpv1.Disruption{
					ConsolidationPolicy: karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized,
					// Disable Consolidation until we're ready
					ConsolidateAfter: karpv1.MustParseNillableDuration("Never"),
				},
				Template: karpv1.NodeClaimTemplate{
					Spec: karpv1.NodeClaimTemplateSpec{
						Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
							{
								NodeSelectorRequirement: corev1.NodeSelectorRequirement{
									Key:      karpv1.CapacityTypeLabelKey,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{karpv1.CapacityTypeOnDemand},
								},
							},
							{
								NodeSelectorRequirement: corev1.NodeSelectorRequirement{
									Key:      v1beta1.LabelSKUCPU,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"2"},
								},
							},
							{
								NodeSelectorRequirement: corev1.NodeSelectorRequirement{
									Key:      v1beta1.LabelSKUFamily,
									Operator: corev1.NodeSelectorOpNotIn,
									// remove some cheap burstable types so we have more control over what gets provisioned
									Values: []string{"B"},
								},
							},
						},
						NodeClassRef: &karpv1.NodeClassReference{
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
							Name:  nodeClass.Name,
						},
					},
				},
			},
		})
		nodePool = env.AdaptToClusterConfig(nodePool)

		var numPods int32 = 2
		smallDep := coretest.Deployment(coretest.DeploymentOptions{
			Replicas: numPods,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "small-app"},
				},
				TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       corev1.LabelHostname,
						WhenUnsatisfiable: corev1.DoNotSchedule,
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "small-app",
							},
						},
					},
				},
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: func() resource.Quantity {
						dsOverhead := env.GetDaemonSetOverhead(nodePool)
						base := lo.ToPtr(resource.MustParse("1800m"))
						base.Sub(*dsOverhead.Cpu())
						return *base
					}(),
					},
				},
			},
		})

		selector := labels.SelectorFromSet(smallDep.Spec.Selector.MatchLabels)
		env.ExpectCreatedNodeCount("==", 0)
		env.ExpectCreated(nodePool, nodeClass, smallDep)

		env.EventuallyExpectHealthyPodCount(selector, int(numPods))
		env.ExpectCreatedNodeCount("==", int(numPods))

		// Enable spot capacity type after the on-demand node is provisioned
		// Expect the node to consolidate to a spot instance as it will be a cheaper
		// instance than on-demand
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		coretest.ReplaceRequirements(nodePool,
			karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: corev1.NodeSelectorOpExists,
				},
			},
			karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1beta1.LabelSKUCPU,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"2"},
				},
			},
		)
		env.ExpectUpdated(nodePool)

		// Eventually expect the on-demand nodes to be consolidated into
		// spot nodes after some time
		Eventually(func(g Gomega) {
			var nodes corev1.NodeList
			Expect(env.Client.List(env.Context, &nodes)).To(Succeed())
			var spotNodes []*corev1.Node
			var otherNodes []*corev1.Node
			for i, n := range nodes.Items {
				// only count the nodes created by the nodePool
				if n.Labels[karpv1.NodePoolLabelKey] != nodePool.Name {
					continue
				}
				if n.Labels[karpv1.CapacityTypeLabelKey] == karpv1.CapacityTypeSpot {
					spotNodes = append(spotNodes, &nodes.Items[i])
				} else {
					otherNodes = append(otherNodes, &nodes.Items[i])
				}
			}
			// all the on-demand nodes should have been replaced with spot nodes
			msg := fmt.Sprintf("node names, spot= %v, other = %v", common.NodeNames(spotNodes), common.NodeNames(otherNodes))
			g.Expect(len(spotNodes)).To(BeNumerically("==", numPods), msg)
			// and we should have no other nodes
			g.Expect(len(otherNodes)).To(BeNumerically("==", 0), msg)
		}, time.Minute*10).Should(Succeed())

		env.ExpectDeleted(smallDep)
	})
})
