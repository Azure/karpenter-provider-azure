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

package scheduling_test

import (
	"fmt"
	"testing"

	"github.com/awslabs/operatorpkg/object"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/test"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/test/pkg/debug"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

func TestScheduling(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "Scheduling")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Scheduling", Ordered, ContinueOnFailure, func() {
	var selectors sets.Set[string]

	BeforeEach(func() {
		// Make the NodePool requirements fully flexible, so we can match well-known label keys
		nodePool = test.ReplaceRequirements(nodePool,
			karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1beta1.LabelSKUFamily,
					Operator: corev1.NodeSelectorOpExists,
				},
			},
		)
	})
	BeforeAll(func() {
		// populate the initial set with selectors that won't be tested
		selectors = sets.New(
			// we don't support Windows yet
			corev1.LabelWindowsBuild,
			// VM SKU with GPU we are using does not populate this; won't be tested
			v1beta1.LabelSKUGPUName,
			// TODO: review the use of "kubernetes.azure.com/cluster"
			v1beta1.AKSLabelCluster,
		)

		// If no spec with Label("GPU") ran (e.g., `-label-filter='!GPU'`),
		// ignore GPU labels in the coverage assertion.
		if !Label("GPU").MatchesLabelFilter(GinkgoLabelFilter()) {
			selectors.Insert(
				v1beta1.LabelSKUGPUCount,
				v1beta1.LabelSKUGPUManufacturer,
			)
		}

	})
	AfterAll(func() {
		// Ensure that we're exercising all well known labels (with the above exceptions)
		Expect(lo.Keys(selectors)).To(ContainElements(append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)))
	})

	It("should apply annotations to the node", func() {
		nodePool.Spec.Template.Annotations = map[string]string{
			"foo":                            "bar",
			karpv1.DoNotDisruptAnnotationKey: "true",
		}
		pod := test.Pod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
		Expect(env.GetNode(pod.Spec.NodeName).Annotations).To(And(HaveKeyWithValue("foo", "bar"), HaveKeyWithValue(karpv1.DoNotDisruptAnnotationKey, "true")))
	})

	Context("Labels", func() {
		It("should support well-known labels for instance type selection", func() {
			nodeSelector := map[string]string{
				// Well Known
				karpv1.NodePoolLabelKey:        nodePool.Name,
				corev1.LabelInstanceTypeStable: "Standard_D2s_v3",
				// Well Known to Azure
				v1beta1.LabelSKUName:                      "Standard_D2s_v3",
				v1beta1.LabelSKUFamily:                    "D",
				v1beta1.LabelSKUSeries:                    "Ds_v3",
				v1beta1.LabelSKUVersion:                   "3",
				v1beta1.LabelSKUCPU:                       "2",
				v1beta1.LabelSKUMemory:                    "8192",
				v1beta1.AKSLabelCPU:                       "2",
				v1beta1.AKSLabelMemory:                    "8192",
				v1beta1.LabelSKUAcceleratedNetworking:     "true",
				v1beta1.LabelSKUStoragePremiumCapable:     "true",
				v1beta1.LabelSKUStorageEphemeralOSMaxSize: "53",
			}
			selectors.Insert(lo.Keys(nodeSelector)...) // Add node selector keys to selectors used in testing to ensure we test all labels
			requirements := lo.MapToSlice(nodeSelector, func(key string, value string) corev1.NodeSelectorRequirement {
				return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
			})
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
				NodeSelector:     nodeSelector,
				NodePreferences:  requirements,
				NodeRequirements: requirements,
			}})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("==", 1)
		})
		It("should support well-known deprecated labels -- beta.kubernetes.io/instance-type", func() {
			// NOTE: this isn't tested alongside the rest of the deprecated labels, because the restriction for
			// instance type + zone is flaky when receiving zonal allocation errors from azure
			// by splitting out this test, we avoid some test flake
			nodeSelector := map[string]string{
				// Deprecated Labels
				corev1.LabelInstanceType: "Standard_D4s_v5",
			}
			selectors.Insert(lo.Keys(nodeSelector)...) // Add node selector keys to selectors used in testing to ensure we test all labels
			requirements := lo.MapToSlice(nodeSelector, func(key string, value string) corev1.NodeSelectorRequirement {
				return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
			})
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
				NodeSelector:     nodeSelector,
				NodePreferences:  requirements,
				NodeRequirements: requirements,
			}})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("==", 1)

		})
		It("should support well-known deprecated labels", func() {
			nodeSelector := map[string]string{
				// Deprecated Labels
				corev1.LabelFailureDomainBetaRegion: env.Region,
				corev1.LabelFailureDomainBetaZone:   fmt.Sprintf("%s-1", env.Region),
				"topology.disk.csi.azure.com/zone":  fmt.Sprintf("%s-1", env.Region),
				"beta.kubernetes.io/arch":           "amd64",
				"beta.kubernetes.io/os":             "linux",
			}
			selectors.Insert(lo.Keys(nodeSelector)...) // Add node selector keys to selectors used in testing to ensure we test all labels
			requirements := lo.MapToSlice(nodeSelector, func(key string, value string) corev1.NodeSelectorRequirement {
				return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
			})
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
				NodeSelector:     nodeSelector,
				NodePreferences:  requirements,
				NodeRequirements: requirements,
			}})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("==", 1)
		})
		It("should support well-known labels for topology and architecture", func() {
			nodeSelector := map[string]string{
				// Well Known
				karpv1.NodePoolLabelKey:     nodePool.Name,
				corev1.LabelTopologyRegion:  env.Region,
				corev1.LabelTopologyZone:    fmt.Sprintf("%s-1", env.Region),
				corev1.LabelOSStable:        "linux",
				corev1.LabelArchStable:      "amd64",
				karpv1.CapacityTypeLabelKey: karpv1.CapacityTypeOnDemand,
			}
			selectors.Insert(lo.Keys(nodeSelector)...) // Add node selector keys to selectors used in testing to ensure we test all labels
			requirements := lo.MapToSlice(nodeSelector, func(key string, value string) corev1.NodeSelectorRequirement {
				return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
			})
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
				NodeSelector:     nodeSelector,
				NodePreferences:  requirements,
				NodeRequirements: requirements,
			}})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("==", 1)
		})
		// note: this test can fail on subscription that don't have quota for GPU SKUs
		It("should support well-known labels for a gpu (nvidia)", Label("GPU"), func() {
			nodeSelector := map[string]string{
				v1beta1.LabelSKUGPUManufacturer: "nvidia",
				v1beta1.LabelSKUGPUCount:        "1",
			}
			selectors.Insert(lo.Keys(nodeSelector)...) // Add node selector keys to selectors used in testing to ensure we test all labels
			requirements := lo.MapToSlice(nodeSelector, func(key string, value string) corev1.NodeSelectorRequirement {
				return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
			})
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
				NodeSelector:     nodeSelector,
				NodePreferences:  requirements,
				NodeRequirements: requirements,
			}})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("==", 1)
		})

		DescribeTable("should support restricted label domain exceptions", func(domain string) {
			// Assign labels to the nodepool so that it has known values
			test.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{NodeSelectorRequirement: corev1.NodeSelectorRequirement{Key: domain + "/team", Operator: corev1.NodeSelectorOpExists}},
				karpv1.NodeSelectorRequirementWithMinValues{NodeSelectorRequirement: corev1.NodeSelectorRequirement{Key: domain + "/custom-label", Operator: corev1.NodeSelectorOpExists}},
				karpv1.NodeSelectorRequirementWithMinValues{NodeSelectorRequirement: corev1.NodeSelectorRequirement{Key: "subdomain." + domain + "/custom-label", Operator: corev1.NodeSelectorOpExists}},
			)
			nodeSelector := map[string]string{
				domain + "/team":                        "team-1",
				domain + "/custom-label":                "custom-value",
				"subdomain." + domain + "/custom-label": "custom-value",
			}
			selectors.Insert(lo.Keys(nodeSelector)...) // Add node selector keys to selectors used in testing to ensure we test all labels
			requirements := lo.MapToSlice(nodeSelector, func(key string, value string) corev1.NodeSelectorRequirement {
				return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
			})
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
				NodeSelector:     nodeSelector,
				NodePreferences:  requirements,
				NodeRequirements: requirements,
			}})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			node := env.ExpectCreatedNodeCount("==", 1)[0]
			// Ensure that the requirements/labels specified above are propagated onto the node
			for k, v := range nodeSelector {
				Expect(node.Labels).To(HaveKeyWithValue(k, v))
			}
		},
			Entry("node-restriction.kubernetes.io", "node-restriction.kubernetes.io"),
			Entry("node.kubernetes.io", "node.kubernetes.io"),
		)
	})

	Context("Provisioning", func() {
		It("should provision a node for naked pods", func() {
			pod := test.Pod()

			env.ExpectCreated(nodeClass, nodePool, pod)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)
		})
		It("should provision a node for a deployment", Label(debug.NoWatch), Label(debug.NoEvents), func() {
			deployment := test.Deployment(test.DeploymentOptions{Replicas: 50})
			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("<=", 2) // should probably all land on a single node, but at worst two depending on batching
		})
		It("should provision a node for a self-affinity deployment", func() {
			// just two pods as they all need to land on the same node
			podLabels := map[string]string{"test": "self-affinity"}
			deployment := test.Deployment(test.DeploymentOptions{
				Replicas: 2,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: podLabels,
					},
					PodRequirements: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
							TopologyKey:   corev1.LabelHostname,
						},
					},
				},
			})

			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), 2)
			env.ExpectCreatedNodeCount("==", 1)
		})
		It("should provision three nodes for a zonal topology spread", func() {

			// one pod per zone
			podLabels := map[string]string{"test": "zonal-spread"}
			deployment := test.Deployment(test.DeploymentOptions{
				Replicas: 3,
				PodOptions: test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: podLabels,
					},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
						{
							MaxSkew:           1,
							TopologyKey:       corev1.LabelTopologyZone,
							WhenUnsatisfiable: corev1.DoNotSchedule,
							LabelSelector:     &metav1.LabelSelector{MatchLabels: podLabels},
							MinDomains:        lo.ToPtr(int32(3)),
							NodeTaintsPolicy:  lo.ToPtr(corev1.NodeInclusionPolicyHonor),
						},
					},
				},
			})

			env.ExpectCreated(nodeClass, nodePool, deployment)
			env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(podLabels), 3)
			// Karpenter will launch three nodes, however if all three nodes don't get register with the cluster at the same time, two pods will be placed on one node.
			// This can result in a case where all 3 pods are healthy, while there are only two created nodes.
			// In that case, we still expect to eventually have three nodes.
			env.EventuallyExpectNodeCount("==", 3)
		})
		It("should provision a node using a NodePool with higher priority", func() {
			nodePoolLowPri := test.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Weight: lo.ToPtr(int32(10)),
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							NodeClassRef: &karpv1.NodeClassReference{
								Group: object.GVK(nodeClass).Group,
								Kind:  object.GVK(nodeClass).Kind,
								Name:  nodeClass.Name,
							},
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      corev1.LabelOSStable,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{string(corev1.Linux)},
									},
								},
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      corev1.LabelInstanceTypeStable,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"Standard_D2s_v5"},
									},
								},
							},
						},
					},
				},
			})
			nodePoolHighPri := test.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Weight: lo.ToPtr(int32(100)),
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							NodeClassRef: &karpv1.NodeClassReference{
								Group: object.GVK(nodeClass).Group,
								Kind:  object.GVK(nodeClass).Kind,
								Name:  nodeClass.Name,
							},
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      corev1.LabelOSStable,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{string(corev1.Linux)},
									},
								},
								{
									NodeSelectorRequirement: corev1.NodeSelectorRequirement{
										Key:      corev1.LabelInstanceTypeStable,
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"Standard_D4s_v5"},
									},
								},
							},
						},
					},
				},
			})
			nodePoolLowPri = env.AdaptToClusterConfig(nodePoolLowPri)
			nodePoolHighPri = env.AdaptToClusterConfig(nodePoolHighPri)

			pod := test.Pod()
			env.ExpectCreated(pod, nodeClass, nodePoolLowPri, nodePoolHighPri)
			env.EventuallyExpectHealthy(pod)
			env.ExpectCreatedNodeCount("==", 1)
			Expect(env.GetVMSKU(pod.Spec.NodeName)).To(Equal("Standard_D4s_v5"))
			Expect(env.GetNode(pod.Spec.NodeName).Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePoolHighPri.Name))
		})

		DescribeTable(
			"should provision a right-sized node when a pod has InitContainers (cpu)",
			func(expectedNodeCPU string, containerRequirements corev1.ResourceRequirements, initContainers ...corev1.Container) {
				if env.K8sMinorVersion() < 29 {
					Skip("native sidecar containers are only enabled on AKS 1.29+")
				}

				labels := map[string]string{"test": test.RandomName()}
				// Create a buffer pod to even out the total resource requests regardless of the daemonsets on the cluster. Assumes
				// CPU is the resource in contention and that total daemonset CPU requests <= 3.
				dsBufferPod := test.Pod(test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: labels,
					},
					PodRequirements: []corev1.PodAffinityTerm{{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labels,
						},
						TopologyKey: corev1.LabelHostname,
					}},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: func() resource.Quantity {
								dsOverhead := env.GetDaemonSetOverhead(nodePool)
								base := lo.ToPtr(resource.MustParse("3"))
								base.Sub(*dsOverhead.Cpu())
								return *base
							}(),
						},
					},
				})

				test.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUCPU,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"4", "8"},
					},
				}, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUFamily,
						Operator: corev1.NodeSelectorOpNotIn,
						// remove some cheap burstable types so we have more control over what gets provisioned
						Values: []string{"B"},
					},
				})
				pod := test.Pod(test.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: labels,
					},
					PodRequirements: []corev1.PodAffinityTerm{{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labels,
						},
						TopologyKey: corev1.LabelHostname,
					}},
					InitContainers:       initContainers,
					ResourceRequirements: containerRequirements,
				})
				env.ExpectCreated(nodePool, nodeClass, dsBufferPod, pod)
				env.EventuallyExpectHealthy(pod)
				node := env.ExpectCreatedNodeCount("==", 1)[0]
				Expect(node.ObjectMeta.GetLabels()[v1beta1.LabelSKUCPU]).To(Equal(expectedNodeCPU))
			},
			Entry("sidecar requirements + later init requirements do exceed container requirements", "8", corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("400m")},
			}, ephemeralInitContainer(corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")},
			}), corev1.Container{
				RestartPolicy: lo.ToPtr(corev1.ContainerRestartPolicyAlways),
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("350m")},
				},
			}, ephemeralInitContainer(corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
			})),
			Entry("sidecar requirements + later init requirements do not exceed container requirements", "4", corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("400m")},
			}, ephemeralInitContainer(corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")},
			}), corev1.Container{
				RestartPolicy: lo.ToPtr(corev1.ContainerRestartPolicyAlways),
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("350m")},
				},
			}, ephemeralInitContainer(corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")},
			})),
			Entry("init container requirements exceed all later requests", "8", corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("400m")},
			}, corev1.Container{
				RestartPolicy: lo.ToPtr(corev1.ContainerRestartPolicyAlways),
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			}, ephemeralInitContainer(corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1500m")},
			}), corev1.Container{
				RestartPolicy: lo.ToPtr(corev1.ContainerRestartPolicyAlways),
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			}),
		)
		It("should provision a right-sized node when a pod has InitContainers (mixed resources)", func() {
			if env.K8sMinorVersion() < 29 {
				Skip("native sidecar containers are only enabled on AKS 1.29+")
			}
			pod := test.Pod(test.PodOptions{
				InitContainers: []corev1.Container{
					{
						RestartPolicy: lo.ToPtr(corev1.ContainerRestartPolicyAlways),
						Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						}},
					},
					ephemeralInitContainer(corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("50m"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}}),
				},
				ResourceRequirements: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				}},
			})
			env.ExpectCreated(nodePool, nodeClass, pod)
			env.EventuallyExpectHealthy(pod)
		})
	})

})

func ephemeralInitContainer(requirements corev1.ResourceRequirements) corev1.Container {
	return corev1.Container{
		Image:     azure.EphemeralInitContainerImage,
		Command:   []string{"/bin/sh"},
		Args:      []string{"-c", "sleep 5"},
		Resources: requirements,
	}
}
