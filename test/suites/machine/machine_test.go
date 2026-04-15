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

package machine_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/blang/semver/v4"
	"github.com/samber/lo"
)

var _ = Describe("Machine Tests", func() {
	var dep *appsv1.Deployment
	var selector labels.Selector
	var numPods int32
	BeforeEach(func() {
		numPods = 1
		// Add pods with a do-not-disrupt annotation so that we can check node metadata before we disrupt
		dep = coretest.Deployment(coretest.DeploymentOptions{
			Replicas: numPods,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "my-app",
					},
					Annotations: map[string]string{
						karpv1.DoNotDisruptAnnotationKey: "true",
					},
				},
				// Each node has 8 cpus, so should fit 2 pods.
				ResourceRequirements: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		})
		selector = labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
	})

	It("should have networking labels applied by machine api", func() {
		// Check if networking settings are compatible with this test
		// NETWORK_PLUGIN_MODE must be overlay, but NETWORK_PLUGIN and NETWORK_DATAPLANE
		// can be unset (using defaults) or set to expected values (but not set to different values)
		settings := env.ExpectSettings()

		// Helper function to check if env var is unset or set to expected value
		checkEnvVar := func(envName, expectedValue string) bool {
			for _, env := range settings {
				if env.Name == envName {
					return env.Value == expectedValue
				}
			}
			return true // Not set is acceptable
		}

		usingCompatiblePlugin := checkEnvVar("NETWORK_PLUGIN", consts.NetworkPluginAzure)
		usingExpectedPluginMode := lo.Contains(settings, corev1.EnvVar{Name: "NETWORK_PLUGIN_MODE", Value: consts.NetworkPluginModeOverlay})
		usingCompatibleDataplane := checkEnvVar("NETWORK_DATAPLANE", consts.NetworkDataplaneCilium)

		if !usingCompatiblePlugin || !usingExpectedPluginMode || !usingCompatibleDataplane {
			Skip("TODO: generalize test for any networking configuration. Skipping as not in expected config for the test")
		}

		env.ExpectCreated(nodeClass, nodePool, dep)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
		env.EventuallyExpectCreatedMachineCount("==", 1)
		env.EventuallyExpectHealthyPodCount(selector, int(numPods))

		// Check that the node has the expected networking labels
		Expect(node.Labels).To(HaveKeyWithValue("kubernetes.azure.com/ebpf-dataplane", consts.NetworkDataplaneCilium))
		Expect(node.Labels).To(HaveKeyWithValue("kubernetes.azure.com/azure-cni-overlay", "true"))
		Expect(node.Labels).To(HaveKeyWithValue("kubernetes.azure.com/podnetwork-type", consts.NetworkPluginModeOverlay))

		// Note: these labels we only check their keys since the values are dynamic
		// TODO: improve E2E test to be dynamic, reusing the same provisioning logic we have for labels creation.
		Expect(lo.Keys(node.Labels)).To(ContainElements([]string{
			"kubernetes.azure.com/network-subnet",
			"kubernetes.azure.com/nodenetwork-vnetguid",
			"kubernetes.azure.com/network-stateless-cni",
		}))
	})

	// NOTE: ClusterTests modify the actual cluster itself, which means that performing tests after a cluster test
	// might not have a clean environment, and might produce unexpected results. Ordering of cluster tests is important.
	// The cluster modification is safe in CI as each test runs in its own cluster.
	Context("ClusterTests", func() {
		BeforeEach(func() {
			// Add labels to nodepool to ensure pods land on Karpenter nodes
			nodePool.Spec.Template.Labels = lo.Assign(nodePool.Spec.Template.Labels, map[string]string{
				"test-name": "karpenter-machine-test",
			})
			// Add nodeSelector to deployment to target Karpenter nodes
			dep.Spec.Template.Spec.NodeSelector = map[string]string{
				"test-name": "karpenter-machine-test",
			}
		})

		It("use the DriftAction to drift nodes that have had their kubeletidentity updated", func() {
			// Check if cluster supports custom kubelet identity (requires user-assigned managed identity)
			if !env.IsClusterUserAssignedIdentity(env.Context) {
				Skip(fmt.Sprintf("Cluster uses %s identity type, but custom kubelet identity requires UserAssigned identity type",
					env.CheckClusterIdentityType(env.Context)))
			}

			numPods = 6
			dep.Spec.Replicas = &numPods
			nodePool = coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelSKUCPU,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"8"},
				},
			)
			env.ExpectCreated(nodeClass, nodePool, dep)

			nodes := env.EventuallyExpectCreatedNodeCount("==", 3)
			nodeClaims := env.EventuallyExpectRegisteredNodeClaimCount("==", 3)
			machines := env.EventuallyExpectCreatedMachineCount("==", 3)
			pods := env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			for _, machine := range machines {
				if machine.Properties.Status.DriftAction != nil {
					Expect(*machine.Properties.Status.DriftAction).ToNot(Equal(containerservice.DriftActionRecreate))
				}
			}

			By("getting the original kubelet identity")
			originalKubeletIdentity := env.GetKubeletIdentity(env.Context)

			By("creating a new managed identity for testing")
			newIdentityName := test.RandomName("karpenter-test-identity")
			newIdentity := env.ExpectCreatedManagedIdentity(env.Context, newIdentityName)

			By("granting ACR access to the new kubelet identity")
			env.ExpectGrantedACRAccess(env.Context, newIdentity)

			By("updating the kubelet identity on the managed cluster")
			poller := env.ExpectUpdatedManagedClusterKubeletIdentityAsync(env.Context, newIdentity)

			By("verifying the kubelet identity was updated")
			updatedKubeletIdentity := env.GetKubeletIdentity(env.Context)
			Expect(updatedKubeletIdentity.ResourceID).To(Equal(newIdentity.ID), "Expected updatedKubeletIdentityResourceID to match new kubelet resource id")
			Expect(updatedKubeletIdentity.ResourceID).ToNot(Equal(originalKubeletIdentity.ResourceID), "Expected updatedKubeletIdentityResourceID to not match old kubelet resource id")

			By("expect machines to have a DriftAction")
			Eventually(func(g Gomega) {
				machines := env.EventuallyExpectCreatedMachineCount("==", 3)
				for _, machine := range machines {
					g.Expect(machine.Properties.Status.DriftAction).ToNot(BeNil())
					g.Expect(*machine.Properties.Status.DriftAction).To(Equal(containerservice.DriftActionRecreate))
				}
			}).WithTimeout(3 * time.Minute).Should(Succeed())

			By("expecting nodes to drift")
			env.EventuallyExpectDriftedWithTimeout(5*time.Minute, nodeClaims...)

			By("remove do-not-disrupt annotation and expect pods to be rescheduled on new nodes")
			for _, pod := range pods {
				delete(pod.Annotations, karpv1.DoNotDisruptAnnotationKey)
				env.ExpectUpdated(pod)
			}
			env.EventuallyExpectNotFound(lo.Map(pods, func(p *corev1.Pod, _ int) client.Object { return p })...)
			env.EventuallyExpectNotFound(lo.Map(nodes, func(n *corev1.Node, _ int) client.Object { return n })...)
			env.EventuallyExpectNotFound(lo.Map(nodeClaims, func(n *karpv1.NodeClaim, _ int) client.Object { return n })...)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))
			env.EventuallyExpectCreatedNodeCount("==", 3)
			env.EventuallyExpectRegisteredNodeClaimCount("==", 3)
			env.EventuallyExpectCreatedMachineCount("==", 3)

			// Make sure to wait til cluster is done updating before proceeding to next test
			_, err := poller.PollUntilDone(env.Context, nil)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should be able to scale machines during an ongoing managed cluster operation", func() {
			// Create two NodePools: one for scale-up, one for scale-down
			scaleUpNodePool := coretest.ReplaceRequirements(env.DefaultNodePool(nodeClass),
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelSKUCPU,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"8"},
				},
			)
			scaleUpNodePool.Spec.Template.Labels = lo.Assign(scaleUpNodePool.Spec.Template.Labels, map[string]string{
				"test-role": "scale-up",
			})

			scaleDownNodePool := coretest.ReplaceRequirements(env.DefaultNodePool(nodeClass),
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelSKUCPU,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"8"},
				},
			)
			scaleDownNodePool.Spec.Template.Labels = lo.Assign(scaleDownNodePool.Spec.Template.Labels, map[string]string{
				"test-role": "scale-down",
			})
			scaleDownNodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
			// Taint scale-down nodes so that only our scale-down pods (which tolerate this taint) can land here.
			// Without this, cluster workloads like metrics-server can get scheduled on these nodes and
			// prevent consolidation after the deployment scales to 0.
			scaleDownNodePool.Spec.Template.Spec.Taints = append(scaleDownNodePool.Spec.Template.Spec.Taints, corev1.Taint{
				Key:    "test-role",
				Value:  "scale-down",
				Effect: corev1.TaintEffectNoSchedule,
			})

			// Scale-up deployment: starts at 2 pods, will scale to 4
			var scaleUpPodCount int32 = 2
			scaleUpDep := coretest.Deployment(coretest.DeploymentOptions{
				Replicas: scaleUpPodCount,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "scale-up"},
						Annotations: map[string]string{
							karpv1.DoNotDisruptAnnotationKey: "true",
						},
					},
					NodeSelector: map[string]string{"test-role": "scale-up"},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
					TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
				},
			})
			scaleUpSelector := labels.SelectorFromSet(scaleUpDep.Spec.Selector.MatchLabels)

			// Scale-down deployment: starts at 2 pods, will scale to 0
			var scaleDownPodCount int32 = 2
			scaleDownDep := coretest.Deployment(coretest.DeploymentOptions{
				Replicas: scaleDownPodCount,
				PodOptions: coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "scale-down"},
						Annotations: map[string]string{
							karpv1.DoNotDisruptAnnotationKey: "true",
						},
					},
					NodeSelector: map[string]string{"test-role": "scale-down"},
					Tolerations: []corev1.Toleration{{
						Key:      "test-role",
						Value:    "scale-down",
						Operator: corev1.TolerationOpEqual,
						Effect:   corev1.TaintEffectNoSchedule,
					}},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
					TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
				},
			})
			scaleDownSelector := labels.SelectorFromSet(scaleDownDep.Spec.Selector.MatchLabels)

			scaleUpNodeSelector := labels.SelectorFromSet(map[string]string{"test-role": "scale-up"})
			scaleDownNodeSelector := labels.SelectorFromSet(map[string]string{"test-role": "scale-down"})

			env.ExpectCreated(nodeClass, scaleUpNodePool, scaleDownNodePool, scaleUpDep, scaleDownDep)

			env.EventuallyExpectNodeCountWithSelector("==", 1, scaleUpNodeSelector)
			env.EventuallyExpectNodeCountWithSelector("==", 1, scaleDownNodeSelector)
			env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", 1, scaleUpNodeSelector)
			env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", 1, scaleDownNodeSelector)
			env.EventuallyExpectCreatedMachineCount("==", 2)
			env.EventuallyExpectHealthyPodCount(scaleUpSelector, int(scaleUpPodCount))
			env.EventuallyExpectHealthyPodCount(scaleDownSelector, int(scaleDownPodCount))

			By("Performing a K8s upgrade")
			availableKubernetesUpgrades := env.ExpectSuccessfulGetOfAvailableKubernetesVersionUpgradesForManagedCluster()
			kubernetesUpgradeVersion := *lo.MaxBy(availableKubernetesUpgrades, func(a, b *containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem) bool {
				aK8sVersion := lo.Must(semver.Parse(*a.KubernetesVersion))
				bK8sVersion := lo.Must(semver.Parse(*b.KubernetesVersion))
				return aK8sVersion.GT(bK8sVersion)
			}).KubernetesVersion

			poller := env.ExpectUpgradeOfManagedCluster(kubernetesUpgradeVersion)

			By("Scaling up the scale-up deployment to create new nodes")
			scaleUpPodCount = 4
			scaleUpDep.Spec.Replicas = &scaleUpPodCount
			env.ExpectUpdated(scaleUpDep)

			By("Scaling down the scale-down deployment to 0")
			scaleDownPodCount = 0
			scaleDownDep.Spec.Replicas = &scaleDownPodCount
			env.ExpectUpdated(scaleDownDep)

			env.EventuallyExpectNodeCountWithSelector("==", 2, scaleUpNodeSelector)
			env.EventuallyExpectNodeCountWithSelector("==", 0, scaleDownNodeSelector)
			env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", 2, scaleUpNodeSelector)
			env.EventuallyExpectRegisteredNodeClaimCountWithSelector("==", 0, scaleDownNodeSelector)
			env.EventuallyExpectCreatedMachineCount("==", 2)
			env.EventuallyExpectHealthyPodCount(scaleUpSelector, int(scaleUpPodCount))
			env.EventuallyExpectHealthyPodCount(scaleDownSelector, int(scaleDownPodCount))

			env.ExpectClusterProvisioningState("Upgrading")

			By("Removing do-not-disrupt annotations to allow Karpenter to update the nodes the pods are on")
			scaleUpPods := env.ExpectPodsMatchingSelector(scaleUpSelector)
			for _, pod := range scaleUpPods {
				delete(pod.Annotations, karpv1.DoNotDisruptAnnotationKey)
				env.ExpectUpdated(pod)
			}

			By("Waiting for cluster upgrade to complete")
			upgradeCtx, cancel := context.WithTimeout(env.Context, 30*time.Minute)
			defer cancel()
			_, err := poller.PollUntilDone(upgradeCtx, nil)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
