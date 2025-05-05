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
	"sync"
	"time"

	"github.com/awslabs/operatorpkg/object"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	azuretest "github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/test/pkg/debug"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
)

const (
	deprovisioningTypeKey = "testing/deprovisioning-type"
	consolidationValue    = "consolidation"
	emptinessValue        = "emptiness"
	expirationValue       = "expiration"
	noExpirationValue     = "noExpiration"
	driftValue            = "drift"
)

const (
	multipleDeprovisionersTestGroup = "multipleDeprovisioners"
	consolidationTestGroup          = "consolidation"
	emptinessTestGroup              = "emptiness"
	expirationTestGroup             = "expiration"
	driftTestGroup                  = "drift"
	interruptionTestGroup           = "interruption"

	defaultTestName = "default"
)

// disableProvisioningLimits represents limits that can be applied to a nodePool if you want a nodePool
// that can deprovision nodes but cannot provision nodes
var disableProvisioningLimits = karpv1.Limits{
	corev1.ResourceCPU:    resource.MustParse("0"),
	corev1.ResourceMemory: resource.MustParse("0Gi"),
}

var _ = Describe("Deprovisioning", Label(debug.NoWatch), Label(debug.NoEvents), func() {
	var nodePool *karpv1.NodePool
	var nodeClass *v1alpha2.AKSNodeClass
	var deployment *appsv1.Deployment
	var deploymentOptions test.DeploymentOptions
	var selector labels.Selector
	var dsCount int

	BeforeEach(func() {
		nodeClass = env.DefaultAKSNodeClass()
		nodePool = env.DefaultNodePool(nodeClass)
		nodePool.Spec.Limits = nil
		test.ReplaceRequirements(nodePool, []karpv1.NodeSelectorRequirementWithMinValues{
			// Ensure that all pods can fit on to the provisioned nodes including all daemonsets
			{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1alpha2.LabelSKUCPU,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"2"},
				},
			},
		}...)
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{
			Nodes: "70%",
		}}
		deploymentOptions = test.DeploymentOptions{
			PodOptions: test.PodOptions{
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("50Mi"),
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		}
		deployment = test.Deployment(deploymentOptions)
		selector = labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		dsCount = env.GetDaemonSetCount(nodePool)
	})

	AfterEach(func() {
		env.Cleanup()
	})

	Context("Multiple Deprovisioners", func() {
		It("should run consolidation, emptiness, expiration, and drift simultaneously", func(_ context.Context) {
			replicasPerNode := 10
			maxPodDensity := replicasPerNode + dsCount
			nodeCountPerNodePool := 4
			replicas := replicasPerNode * nodeCountPerNodePool

			disruptionMethods := []string{
				consolidationValue,
				emptinessValue,
				expirationValue,
				driftValue,
			}
			expectedNodeCount := nodeCountPerNodePool * len(disruptionMethods)

			deploymentMap := map[string]*appsv1.Deployment{}
			// Generate all the deployments for multi-deprovisioning
			for _, v := range disruptionMethods {
				deploymentOptions.PodOptions.NodeSelector = map[string]string{
					deprovisioningTypeKey: v,
				}
				deploymentOptions.PodOptions.Labels = map[string]string{
					deprovisioningTypeKey: v,
				}
				deploymentOptions.PodOptions.Tolerations = []corev1.Toleration{
					{
						Key:      deprovisioningTypeKey,
						Operator: corev1.TolerationOpEqual,
						Value:    v,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				}
				deploymentOptions.Replicas = int32(replicas)
				d := test.Deployment(deploymentOptions)
				deploymentMap[v] = d
			}

			nodePoolMap := map[string]*karpv1.NodePool{}
			// Generate all the nodePools for multi-deprovisioning
			for _, v := range disruptionMethods {
				np := test.NodePool()
				np.Spec = *nodePool.Spec.DeepCopy()
				np.Spec.Template.Spec.Taints = []corev1.Taint{
					{
						Key:    deprovisioningTypeKey,
						Value:  v,
						Effect: corev1.TaintEffectNoSchedule,
					},
				}
				np.Spec.Template.Labels = lo.Assign(np.Spec.Template.Labels, map[string]string{
					deprovisioningTypeKey: v,
				})
				nodePoolMap[v] = test.NodePool(*np)
			}

			By("waiting for the deployment to deploy all of its pods")
			var wg sync.WaitGroup
			for _, d := range deploymentMap {
				wg.Add(1)
				go func(dep *appsv1.Deployment) {
					defer GinkgoRecover()
					defer wg.Done()

					env.ExpectCreated(dep)
					env.EventuallyExpectPendingPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), int(lo.FromPtr(dep.Spec.Replicas)))
				}(d)
			}
			wg.Wait()

			nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))

			// Create a separate nodeClass for drift so that we can change the nodeClass later without it affecting
			// the other nodePools
			driftNodeClass := azuretest.AKSNodeClass()
			driftNodeClass.Spec = *nodeClass.Spec.DeepCopy()
			nodePoolMap[driftValue].Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
				Group: object.GVK(driftNodeClass).Group,
				Kind:  object.GVK(driftNodeClass).Kind,
				Name:  driftNodeClass.Name,
			}
			env.MeasureProvisioningDurationFor(func() {
				By("kicking off provisioning by applying the nodePool and nodeClass")
				env.ExpectCreated(driftNodeClass, nodeClass)
				for _, p := range nodePoolMap {
					env.ExpectCreated(p)
				}

				env.EventuallyExpectCreatedNodeClaimCount("==", expectedNodeCount)
				env.EventuallyExpectCreatedNodeCount("==", expectedNodeCount)
				env.EventuallyExpectInitializedNodeCount("==", expectedNodeCount)

				// Wait for all pods across all deployments we have created to be in a healthy state
				wg = sync.WaitGroup{}
				for _, d := range deploymentMap {
					wg.Add(1)
					go func(dep *appsv1.Deployment) {
						defer GinkgoRecover()
						defer wg.Done()

						env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(dep.Spec.Selector.MatchLabels), int(lo.FromPtr(dep.Spec.Replicas)))
					}(d)
				}
				wg.Wait()
			}, map[string]string{
				azure.TestCategoryDimension:           multipleDeprovisionersTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			By("scaling down replicas across deployments")
			deploymentMap[consolidationValue].Spec.Replicas = lo.ToPtr(int32(int(float64(replicas) * 0.25)))
			deploymentMap[emptinessValue].Spec.Replicas = lo.ToPtr[int32](0)
			for _, d := range deploymentMap {
				env.ExpectUpdated(d)
			}

			// Create a nodePool for expiration so that expiration can do replacement
			nodePoolMap[noExpirationValue] = test.NodePool()
			nodePoolMap[noExpirationValue].Spec = *nodePoolMap[expirationValue].Spec.DeepCopy()

			// Enable consolidation, emptiness, and expiration
			nodePoolMap[consolidationValue].Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
			nodePoolMap[emptinessValue].Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
			nodePoolMap[emptinessValue].Spec.Disruption.ConsolidateAfter.Duration = lo.ToPtr(time.Duration(0))
			nodePoolMap[expirationValue].Spec.Template.Spec.ExpireAfter.Duration = lo.ToPtr(time.Duration(0))
			nodePoolMap[expirationValue].Spec.Limits = disableProvisioningLimits
			// Update the drift NodeClass to start drift on Nodes assigned to this NodeClass
			driftNodeClass.Spec.ImageFamily = lo.ToPtr(v1alpha2.AzureLinuxImageFamily)

			// Create test assertions to ensure during the multiple deprovisioner scale-downs
			type testAssertions struct {
				deletedCount             int
				deletedNodeCountSelector labels.Selector
				nodeCount                int
				nodeCountSelector        labels.Selector
				createdCount             int
			}
			assertionMap := map[string]testAssertions{
				consolidationValue: {
					deletedCount: int(float64(nodeCountPerNodePool) * 0.75),
					nodeCount:    int(float64(nodeCountPerNodePool) * 0.25),
					createdCount: 0,
				},
				emptinessValue: {
					deletedCount: nodeCountPerNodePool,
					nodeCount:    0,
					createdCount: 0,
				},
				expirationValue: {
					deletedCount: nodeCountPerNodePool,
					nodeCount:    nodeCountPerNodePool,
					nodeCountSelector: labels.SelectorFromSet(map[string]string{
						karpv1.NodePoolLabelKey: nodePoolMap[noExpirationValue].Name,
					}),
					createdCount: nodeCountPerNodePool,
				},
				driftValue: {
					deletedCount: nodeCountPerNodePool,
					nodeCount:    nodeCountPerNodePool,
					createdCount: nodeCountPerNodePool,
				},
			}
			totalDeletedCount := lo.Reduce(lo.Values(assertionMap), func(agg int, assertion testAssertions, _ int) int {
				return agg + assertion.deletedCount
			}, 0)
			totalCreatedCount := lo.Reduce(lo.Values(assertionMap), func(agg int, assertion testAssertions, _ int) int {
				return agg + assertion.createdCount
			}, 0)
			env.MeasureDeprovisioningDurationFor(func() {
				By("enabling deprovisioning across nodePools")
				for _, p := range nodePoolMap {
					p.Spec.Disruption.Budgets = []karpv1.Budget{{
						Nodes: "70%",
					}}
					env.ExpectCreatedOrUpdated(p)
				}
				env.ExpectUpdated(driftNodeClass)

				By("waiting for the nodes across all deprovisioners to get deleted")
				wg = sync.WaitGroup{}
				for k, v := range assertionMap {
					wg.Add(1)
					go func(d string, assertions testAssertions) {
						defer GinkgoRecover()
						defer wg.Done()

						By("waiting for type " + k)
						// Provide a default selector based on the original nodePool name if one isn't specified
						selector = assertions.deletedNodeCountSelector
						if selector == nil {
							selector = labels.SelectorFromSet(map[string]string{karpv1.NodePoolLabelKey: nodePoolMap[d].Name})
						}
						env.EventuallyExpectDeletedNodeCountWithSelector("==", assertions.deletedCount, selector)

						// Provide a default selector based on the original nodePool name if one isn't specified
						selector = assertions.nodeCountSelector
						if selector == nil {
							selector = labels.SelectorFromSet(map[string]string{karpv1.NodePoolLabelKey: nodePoolMap[d].Name})
						}
						env.EventuallyExpectNodeCountWithSelector("==", assertions.nodeCount, selector)
						env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deploymentMap[d].Spec.Selector.MatchLabels), int(lo.FromPtr(deploymentMap[d].Spec.Replicas)))
						By("done waiting for type " + k)
					}(k, v)
				}
				wg.Wait()
			}, map[string]string{
				azure.TestCategoryDimension:           multipleDeprovisionersTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(totalCreatedCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(totalDeletedCount),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
	})
	Context("Consolidation", func() {
		It("should delete all empty nodes with consolidation", func(_ context.Context) {
			replicasPerNode := 20
			maxPodDensity := replicasPerNode + dsCount
			expectedNodeCount := 50
			replicas := replicasPerNode * expectedNodeCount

			deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))

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
				azure.TestCategoryDimension:           consolidationTestGroup,
				azure.TestNameDimension:               "empty/delete",
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			// Delete deployment to make nodes empty
			env.ExpectDeleted(deployment)
			env.EventuallyExpectHealthyPodCount(selector, 0)

			env.MeasureDeprovisioningDurationFor(func() {
				By("kicking off deprovisioning by setting the consolidation enabled value on the nodePool")
				nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
				nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
				env.ExpectUpdated(nodePool)

				env.EventuallyExpectDeletedNodeCount("==", expectedNodeCount)
				env.EventuallyExpectNodeCount("==", 0)
			}, map[string]string{
				azure.TestCategoryDimension:           consolidationTestGroup,
				azure.TestNameDimension:               "empty/delete",
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(0),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(expectedNodeCount),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
		It("should consolidate nodes to get a higher utilization (multi-consolidation delete)", func(_ context.Context) {
			replicasPerNode := 20
			maxPodDensity := replicasPerNode + dsCount
			expectedNodeCount := 50
			replicas := replicasPerNode * expectedNodeCount

			deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))

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
				azure.TestCategoryDimension:           consolidationTestGroup,
				azure.TestNameDimension:               "delete",
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			replicas = int(float64(replicas) * 0.2)
			deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
			env.ExpectUpdated(deployment)
			env.EventuallyExpectHealthyPodCount(selector, replicas)

			env.MeasureDeprovisioningDurationFor(func() {
				By("kicking off deprovisioning by setting the consolidation enabled value on the nodePool")
				nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
				nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
				env.ExpectUpdated(nodePool)

				env.EventuallyExpectDeletedNodeCount("==", int(float64(expectedNodeCount)*0.8))
				env.EventuallyExpectNodeCount("==", int(float64(expectedNodeCount)*0.2))
				env.EventuallyExpectHealthyPodCount(selector, replicas)
			}, map[string]string{
				azure.TestCategoryDimension:           consolidationTestGroup,
				azure.TestNameDimension:               "delete",
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(0),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(int(float64(expectedNodeCount) * 0.8)),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
		It("should consolidate nodes to get a higher utilization (single consolidation replace)", func(_ context.Context) {
			replicasPerNode := 1
			// TODO: review and adjust this for Azure provider performance
			expectedNodeCount := 20 // we're currently doing around 1 node/2 mins so this test should run deprovisioning in about 45m
			replicas := replicasPerNode * expectedNodeCount

			// Add in a instance type size requirement that's larger than the smallest that fits the pods.
			test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1alpha2.LabelSKUCPU,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"8"},
				}})

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
				azure.TestCategoryDimension:           consolidationTestGroup,
				azure.TestNameDimension:               "replace",
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			env.MeasureDeprovisioningDurationFor(func() {
				By("kicking off deprovisioning by setting the consolidation enabled value on the nodePool")
				// The nodePool defaults to a larger instance type than we need so enabling consolidation and making
				// the requirements wide-open should cause deletes and increase our utilization on the cluster
				nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
				nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
				nodePool.Spec.Template.Spec.Requirements = lo.Reject(nodePool.Spec.Template.Spec.Requirements, func(r karpv1.NodeSelectorRequirementWithMinValues, _ int) bool {
					return r.Key == v1alpha2.LabelSKUCPU
				})
				env.ExpectUpdated(nodePool)

				env.EventuallyExpectDeletedNodeCount("==", expectedNodeCount) // every node should delete due to replacement
				env.EventuallyExpectNodeCount("==", expectedNodeCount)
				env.EventuallyExpectHealthyPodCount(selector, replicas)
			}, map[string]string{
				azure.TestCategoryDimension:           consolidationTestGroup,
				azure.TestNameDimension:               "replace",
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(expectedNodeCount),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
	})
	Context("Emptiness", func() {
		It("should deprovision all nodes when empty", func(_ context.Context) {
			replicasPerNode := 20
			maxPodDensity := replicasPerNode + dsCount
			expectedNodeCount := 50
			replicas := replicasPerNode * expectedNodeCount

			deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))

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
				azure.TestCategoryDimension:           emptinessTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			By("waiting for all deployment pods to be deleted")
			// Delete deployment to make nodes empty
			env.ExpectDeleted(deployment)
			env.EventuallyExpectHealthyPodCount(selector, 0)

			env.MeasureDeprovisioningDurationFor(func() {
				By("kicking off deprovisioning emptiness by setting the ttlSecondsAfterEmpty value on the nodePool")
				nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
				nodePool.Spec.Disruption.ConsolidateAfter.Duration = lo.ToPtr(time.Duration(0))
				env.ExpectCreatedOrUpdated(nodePool)

				env.EventuallyExpectDeletedNodeCount("==", expectedNodeCount)
				env.EventuallyExpectNodeCount("==", 0)
			}, map[string]string{
				azure.TestCategoryDimension:           emptinessTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(0),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(expectedNodeCount),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
	})
	Context("Expiration", func() {
		It("should expire all nodes", func(_ context.Context) {
			replicasPerNode := 20
			maxPodDensity := replicasPerNode + dsCount
			// TODO: review and adjust this for Azure provider performance
			expectedNodeCount := 20 // we're currently doing around 1 node/2 mins so this test should run deprovisioning in about 45m
			replicas := replicasPerNode * expectedNodeCount

			deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))
			// Enable Expiration
			nodePool.Spec.Template.Spec.ExpireAfter = karpv1.MustParseNillableDuration("5m")

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
				azure.TestCategoryDimension:           expirationTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			env.MeasureDeprovisioningDurationFor(func() {
				By("kicking off deprovisioning expiration by setting the ttlSecondsUntilExpired value on the nodePool")
				// Change limits so that replacement nodes will use another nodePool.
				nodePool.Spec.Limits = disableProvisioningLimits

				noExpireNodePool := test.NodePool(*nodePool.DeepCopy())

				// Disable Expiration
				noExpireNodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")
				noExpireNodePool.Spec.Template.Spec.ExpireAfter.Duration = nil

				noExpireNodePool.ObjectMeta = metav1.ObjectMeta{Name: test.RandomName()}
				noExpireNodePool.Spec.Limits = nil
				env.ExpectCreatedOrUpdated(nodePool, noExpireNodePool)

				env.EventuallyExpectDeletedNodeCount("==", expectedNodeCount)
				env.EventuallyExpectNodeCount("==", expectedNodeCount)
				env.EventuallyExpectHealthyPodCount(selector, replicas)
			}, map[string]string{
				azure.TestCategoryDimension:           expirationTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(expectedNodeCount),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
	})
	Context("Drift", func() {
		It("should drift all nodes", func(_ context.Context) {
			// Before Deprovisioning, we need to Provision the cluster to the state that we need.
			replicasPerNode := 20
			maxPodDensity := replicasPerNode + dsCount
			// TODO: review and adjust this for Azure provider performance
			expectedNodeCount := 20 // we're currently doing around 1 node/2 mins so this test should run deprovisioning in about 45m
			replicas := replicasPerNode * expectedNodeCount

			deployment.Spec.Replicas = lo.ToPtr(int32(replicas))
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(maxPodDensity))

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
				azure.TestCategoryDimension:           driftTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(0),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})

			env.Monitor.Reset() // Reset the monitor so that we now track the nodes starting at this point in time

			env.MeasureDeprovisioningDurationFor(func() {
				By("kicking off deprovisioning drift by changing the nodeClass ImageFamily")
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1alpha2.AzureLinuxImageFamily)
				env.ExpectCreatedOrUpdated(nodeClass)

				env.EventuallyExpectDeletedNodeCount("==", expectedNodeCount)
				env.EventuallyExpectNodeCount("==", expectedNodeCount)
				env.EventuallyExpectHealthyPodCount(selector, replicas)
			}, map[string]string{
				azure.TestCategoryDimension:           driftTestGroup,
				azure.TestNameDimension:               defaultTestName,
				azure.ProvisionedNodeCountDimension:   strconv.Itoa(expectedNodeCount),
				azure.DeprovisionedNodeCountDimension: strconv.Itoa(expectedNodeCount),
				azure.PodDensityDimension:             strconv.Itoa(replicasPerNode),
			})
		}, SpecTimeout(time.Hour))
	})
})
