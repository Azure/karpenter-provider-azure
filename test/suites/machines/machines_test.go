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

package machines_test

import (
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

	containerservice "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
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

		if !(usingCompatiblePlugin && usingExpectedPluginMode && usingCompatibleDataplane) {
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

		// Note: these labels we only check their keys since, the values are dynamic
		// TODO: improve E2E test to be dynamic, reusing the same provisioning logic we have for labels creation.
		Expect(lo.Keys(node.Labels)).To(ContainElements([]string{
			"kubernetes.azure.com/network-subnet",
			"kubernetes.azure.com/nodenetwork-vnetguid",
			"kubernetes.azure.com/network-stateless-cni",
		}))
	})

	// NOTE: ClusterTests modify the actual cluster itself, which means that preforming tests after a cluster test
	// might not have a clean environment, and might produce unexpected results. Ordering of cluster tests is important
	Context("ClusterTests", func() {
		It("use the DriftAction to drift nodes that have had their kubeletidentity updated", func() {
			// Check if cluster supports custom kubelet identity (requires user-assigned managed identity)
			if !env.IsClusterUserAssignedIdentity(env.Context) {
				Skip(fmt.Sprintf("Cluster uses %s identity type, but custom kubelet identity requires UserAssigned identity type",
					env.CheckClusterIdentityType(env.Context)))
			}

			numPods = 2
			dep.Spec.Replicas = &numPods
			nodePool = coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUCPU,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"8"},
					},
				},
			)
			env.ExpectCreated(nodeClass, nodePool, dep)

			nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
			nodeClaims := env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
			machines := env.EventuallyExpectCreatedMachineCount("==", 1)
			pods := env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			for _, machine := range machines {
				if machine.Properties.Status.DriftAction != nil {
					Expect(*machine.Properties.Status.DriftAction).ToNot(Equal(containerservice.DriftActionRecreate))
				}
			}

			By("getting the original kubelet identity")
			// originalKubeletIdentityResourceID := env.GetKubeletIdentityResourceID(env.Context)

			By("creating a new managed identity for testing")
			newIdentityName := test.RandomName("karpenter-test-identity")
			newIdentity := env.ExpectCreatedManagedIdentity(env.Context, newIdentityName)

			By("updating the kubelet identity on the managed cluster")
			env.ExpectUpdatedManagedClusterKubeletIdentityAsync(env.Context, newIdentity)

			By("granting ACR access to the new kubelet identity")
			env.ExpectGrantedACRAccess(env.Context, newIdentity)

			By("verifying the kubelet identity was updated")
			// updatedKubeletIdentityResourceID := env.GetKubeletIdentityResourceID(env.Context)

			// TODO: check if we want to have this possibly logged
			// Expect(updatedKubeletIdentityResourceID).To(Equal(lo.FromPtr(newIdentity.ID)), "Expected updatedKubeletIdentityResourceID to match new kubelet resource id")
			// Expect(updatedKubeletIdentityResourceID).ToNot(Equal(originalKubeletIdentityResourceID), "Expected updatedKubeletIdentityResourceID to not match old kubelet resource id")

			By("expect machines to have a DriftAction")
			Eventually(func(g Gomega) {
				machines := env.EventuallyExpectCreatedMachineCount("==", 1)
				for _, machine := range machines {
					g.Expect(machine.Properties.Status.DriftAction).ToNot(BeNil())
					g.Expect(*machine.Properties.Status.DriftAction).To(Equal(containerservice.DriftActionRecreate))
				}
			}).WithTimeout(3 * time.Minute).Should(Succeed())

			By("expecting nodes to drift")
			env.EventuallyExpectDriftedWithTimeout(15*time.Minute, nodeClaims...)

			for _, pod := range pods {
				delete(pod.Annotations, karpv1.DoNotDisruptAnnotationKey)
				env.ExpectUpdated(pod)
			}

			env.EventuallyExpectNotFound(lo.Map(pods, func(p *corev1.Pod, _ int) client.Object { return p })...)
			env.EventuallyExpectNotFound(lo.Map(nodes, func(n *corev1.Node, _ int) client.Object { return n })...)
			env.EventuallyExpectNotFound(lo.Map(nodeClaims, func(n *karpv1.NodeClaim, _ int) client.Object { return n })...)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))
			env.EventuallyExpectCreatedNodeCount("==", 1)
			env.EventuallyExpectRegisteredNodeClaimCount("==", 1)
			env.EventuallyExpectCreatedMachineCount("==", 1)
		})

		It("should be able to provision machines during an ongoing managed cluster operation", func() {
			numPods = 6
			dep.Spec.Replicas = &numPods
			nodePool = coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUCPU,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"8"},
					},
				},
			)
			env.ExpectCreated(nodeClass, nodePool, dep)

			env.EventuallyExpectCreatedNodeCount("==", 3)
			env.EventuallyExpectRegisteredNodeClaimCount("==", 3)
			env.EventuallyExpectCreatedMachineCount("==", 3)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			By("Preforming a K8s upgrade")
			availableKubernetesUpgrades := env.ExpectSuccessfulGetOfAvailableKubernetesVersionUpgradesForManagedCluster()
			kubernetesUpgradeVersion := *lo.MaxBy(availableKubernetesUpgrades, func(a, b *containerservice.ManagedClusterPoolUpgradeProfileUpgradesItem) bool {
				aK8sVersion := lo.Must(semver.Parse(*a.KubernetesVersion))
				bK8sVersion := lo.Must(semver.Parse(*b.KubernetesVersion))
				return aK8sVersion.GT(bK8sVersion)
			}).KubernetesVersion
			upgradedMC := env.ExpectSuccessfulUpgradeOfManagedCluster(kubernetesUpgradeVersion)
			Expect(*upgradedMC.Properties.CurrentKubernetesVersion).To(Equal(kubernetesUpgradeVersion))

			By("Scaling the deployment to create new nodes")
			numPods = 10
			dep.Spec.Replicas = &numPods
			env.ExpectCreated(dep)

			env.EventuallyExpectCreatedNodeCount("==", 5)
			env.EventuallyExpectRegisteredNodeClaimCount("==", 5)
			env.EventuallyExpectCreatedMachineCount("==", 5)
			env.EventuallyExpectHealthyPodCount(selector, int(numPods))

			env.WarnIfClusterNotInExpectedProvisioningState("upgrading")
		})
	})
})
