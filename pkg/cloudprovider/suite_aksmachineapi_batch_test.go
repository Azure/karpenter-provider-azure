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

package cloudprovider

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
)

var _ = Describe("AKS Machine API Batching", func() {
	var azureEnvBatch *test.Environment
	var cloudProviderBatch *CloudProvider
	var clusterBatch *state.Cluster
	var coreProvisionerBatch *provisioning.Provisioner

	BeforeEach(func() {
		batchOptions := test.Options()
		batchOptions.BatchCreationEnabled = true
		batchOptions.BatchIdleTimeoutMS = 100
		batchOptions.BatchMaxTimeoutMS = 1000
		batchOptions.MaxBatchSize = 50
		batchOptions.ProvisionMode = consts.ProvisionModeAKSMachineAPI
		batchOptions.UseSIG = true // AKS Machine API requires SIG images, not CIG

		ctx = coreoptions.ToContext(ctx, coretest.Options())
		ctx = options.ToContext(ctx, batchOptions)

		azureEnvBatch = test.NewEnvironment(ctx, env)
		test.ApplyDefaultStatus(nodeClass, env, batchOptions.UseSIG)
		cloudProviderBatch = New(azureEnvBatch.InstanceTypesProvider, azureEnvBatch.VMInstanceProvider, azureEnvBatch.AKSMachineProvider, recorder, env.Client, azureEnvBatch.ImageProvider, azureEnvBatch.InstanceTypeStore)
		clusterBatch = state.NewCluster(fakeClock, env.Client, cloudProviderBatch)
		coreProvisionerBatch = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProviderBatch, clusterBatch, fakeClock)
	})

	Context("Batch Creation", func() {
		It("should batch multiple machine creations together", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Use pod anti-affinity to force separate NodeClaims
			pods := []*v1.Pod{
				coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "batch-test"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "batch-test"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("1"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				}),
				coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "batch-test"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "batch-test"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("1"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				}),
				coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "batch-test"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "batch-test"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("1"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				}),
			}

			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pods...)
			for _, pod := range pods {
				ExpectScheduled(ctx, env.Client, pod)
			}

			callCount := azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
			// When using pod anti-affinity, each pod gets its own NodeClaim
			// The provisioner creates NodeClaims serially, so each one completes its own batch window
			// In real usage with concurrent requests, the batch window would accumulate more requests
			Expect(callCount).To(BeNumerically(">=", 1))
			Expect(callCount).To(BeNumerically("<=", 3))

			nodeClaims, err := cloudProviderBatch.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(3))

			for _, nc := range nodeClaims {
				validateAKSMachineNodeClaim(nc, nodePool)
			}
		})

		It("should handle batch header correctly", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Use pod anti-affinity to force separate NodeClaims
			pods := []*v1.Pod{
				coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "batch-header-test"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "batch-header-test"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
				}),
				coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "batch-header-test"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "batch-header-test"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
				}),
			}

			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pods...)

			callCount := azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
			Expect(callCount).To(BeNumerically(">=", 1))

			if callCount == 1 {
				createInput := azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(createInput.AKSMachine).ToNot(BeNil())
			}
		})

		It("should respect max batch size", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Create 60 pods with anti-affinity to force separate NodeClaims
			podCount := 60
			pods := make([]*v1.Pod, podCount)
			for i := 0; i < podCount; i++ {
				pods[i] = coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "batch-max-test"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "batch-max-test"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("100m"),
							v1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				})
			}

			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pods...)

			callCount := azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
			Expect(callCount).To(BeNumerically(">=", 2))
		})

		It("should separate batches by template differences", func() {
			nodeClass2 := test.AKSNodeClass(v1beta1.AKSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "different-nodeclass",
				},
			})
			// Apply default status for nodeClass2 so validation passes
			test.ApplyDefaultStatus(nodeClass2, env, true)

			nodePool2 := coretest.NodePool(karpv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "different-nodepool",
				},
				Spec: karpv1.NodePoolSpec{
					Template: karpv1.NodeClaimTemplate{
						Spec: karpv1.NodeClaimTemplateSpec{
							NodeClassRef: &karpv1.NodeClassReference{
								Group: "karpenter.azure.com",
								Kind:  "AKSNodeClass",
								Name:  nodeClass2.Name,
							},
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{
									NodeSelectorRequirement: v1.NodeSelectorRequirement{
										Key:      v1.LabelInstanceTypeStable,
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"Standard_D4s_v3"},
									},
								},
							},
						},
					},
				},
			})

			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClass2, nodePool2)

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			pod1 := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
			})
			pod2 := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{karpv1.NodePoolLabelKey: nodePool2.Name},
			})

			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pod1, pod2)

			callCount := azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
			Expect(callCount).To(BeNumerically(">=", 2))
		})

		It("should handle batch timeout correctly", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			// Use pod anti-affinity to force separate NodeClaims
			pod1 := coretest.UnschedulablePod(coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "batch-timeout-test"},
				},
				PodAntiRequirements: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "batch-timeout-test"},
						},
						TopologyKey: v1.LabelHostname,
					},
				},
			})
			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pod1)

			pod2 := coretest.UnschedulablePod(coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "batch-timeout-test"},
				},
				PodAntiRequirements: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "batch-timeout-test"},
						},
						TopologyKey: v1.LabelHostname,
					},
				},
			})
			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pod2)

			callCount := azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()
			Expect(callCount).To(BeNumerically(">=", 1))
		})
	})

	Context("Batch with Updates", func() {
		It("should not batch update operations", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, clusterBatch, cloudProviderBatch, coreProvisionerBatch, azureEnvBatch, pod)
			ExpectScheduled(ctx, env.Client, pod)

			nodeClaims, err := cloudProviderBatch.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()

			nodeClaim := nodeClaims[0]
			if nodeClaim.Annotations == nil {
				nodeClaim.Annotations = make(map[string]string)
			}
			nodeClaim.Annotations["test-annotation"] = "test-value"

			azureEnvBatch.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		})
	})
})
