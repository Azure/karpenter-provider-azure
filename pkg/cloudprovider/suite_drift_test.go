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

// suite_drift_test.go tests drift detection across provision modes.
// Shared drift tests (image version, k8s version, static fields) are extracted into
// runSharedDriftTests() which is called from each mode's Context. Mode-specific drift
// tests (e.g., DriftAction for AKSMachineAPIHeaderBatch, image gallery change to SIG
// for AKSScriptless) live in their respective mode-specific sub-Contexts.
//
// For instance configuration features, see suite_features_test.go.
// For lifecycle/CRUD operations, see suite_integration_test.go.

import (
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
)

// runSharedDriftTests contains drift tests that exercise IsDrifted() behavior identically
// under both provision modes. The driftNodeClaim and node are set up by the caller's BeforeEach.
//
// These tests verify drift detection logic that is mode-agnostic: the IsDrifted() function
// checks NodeClass status fields (images, k8s version, hash) and node labels — none of which
// depend on how the instance was created.
func runSharedDriftTests(getDriftNodeClaim func() *karpv1.NodeClaim, getNode func() *v1.Node) {
	It("should not fail if nodeClass does not exist", func() {
		ExpectDeleted(ctx, env.Client, nodeClass)
		drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
		Expect(err).ToNot(HaveOccurred())
		Expect(drifted).To(BeEmpty())
	})

	It("should not fail if nodePool does not exist", func() {
		ExpectDeleted(ctx, env.Client, nodePool)
		drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
		Expect(err).ToNot(HaveOccurred())
		Expect(drifted).To(BeEmpty())
	})

	It("should not return drifted if the NodeClaim is valid", func() {
		drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
		Expect(err).ToNot(HaveOccurred())
		Expect(drifted).To(BeEmpty())
	})

	It("should error drift if NodeClaim doesn't have provider id", func() {
		nc := getDriftNodeClaim()
		nc.Status = karpv1.NodeClaimStatus{}
		drifted, err := cloudProvider.IsDrifted(ctx, nc)
		Expect(err).To(HaveOccurred())
		Expect(drifted).To(BeEmpty())
	})

	Context("Node Image Drift", func() {
		It("should succeed with no drift when nothing changes", func() {
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		It("should succeed with no drift when ConditionTypeImagesReady is not true", func() {
			nodeClass = ExpectExists(ctx, env.Client, nodeClass)
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNoLongerReady", "test when images aren't ready")
			ExpectApplied(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		// Note: this case shouldn't be able to happen in practice since if Images is empty ConditionTypeImagesReady should be false.
		It("should error when Images are empty", func() {
			nodeClass = ExpectExists(ctx, env.Client, nodeClass)
			nodeClass.Status.Images = []v1beta1.NodeImage{}
			ExpectApplied(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).To(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		It("should trigger drift when the image version changes", func() {
			test.ApplyCIGImagesWithVersion(nodeClass, "202503.02.0")
			ExpectApplied(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(ImageDrift))
		})
	})

	Context("Kubernetes Version", func() {
		It("should succeed with no drift when nothing changes", func() {
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		It("should succeed with no drift when KubernetesVersionReady is not true", func() {
			nodeClass = ExpectExists(ctx, env.Client, nodeClass)
			nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "K8sVersionNoLongerReady", "test when k8s isn't ready")
			ExpectApplied(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
		It("shouldn't error or be drifted when KubernetesVersion is empty", func() {
			nodeClass = ExpectExists(ctx, env.Client, nodeClass)
			nodeClass.Status.KubernetesVersion = lo.ToPtr("")
			ExpectApplied(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		It("shouldn't error or be drifted when NodeName is missing", func() {
			nc := getDriftNodeClaim()
			nc.Status.NodeName = ""
			drifted, err := cloudProvider.IsDrifted(ctx, nc)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		It("shouldn't error or be drifted when node is not found", func() {
			nc := getDriftNodeClaim()
			nc.Status.NodeName = "NodeWhoDoesNotExist"
			drifted, err := cloudProvider.IsDrifted(ctx, nc)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))
		})

		It("shouldn't error or be drifted when node is deleting", func() {
			nc := getDriftNodeClaim()
			node := ExpectNodeExists(ctx, env.Client, nc.Status.NodeName)
			node.Finalizers = append(node.Finalizers, test.TestingFinalizer)
			ExpectApplied(ctx, env.Client, node)
			Expect(env.Client.Delete(ctx, node)).ToNot(HaveOccurred())
			drifted, err := cloudProvider.IsDrifted(ctx, nc)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NoDrift))

			// cleanup
			node = ExpectNodeExists(ctx, env.Client, nc.Status.NodeName)
			deepCopy := node.DeepCopy()
			node.Finalizers = lo.Reject(node.Finalizers, func(finalizer string, _ int) bool {
				return finalizer == test.TestingFinalizer
			})
			Expect(env.Client.Patch(ctx, node, client.StrategicMergeFrom(deepCopy))).NotTo(HaveOccurred())
			ExpectDeleted(ctx, env.Client, node)
		})

		It("should succeed with drift true when KubernetesVersion is new", func() {
			nodeClass = ExpectExists(ctx, env.Client, nodeClass)

			semverCurrentK8sVersion := lo.Must(semver.ParseTolerant(*nodeClass.Status.KubernetesVersion))
			semverCurrentK8sVersion.Minor = semverCurrentK8sVersion.Minor + 1
			nodeClass.Status.KubernetesVersion = lo.ToPtr(semverCurrentK8sVersion.String())

			ExpectApplied(ctx, env.Client, nodeClass)

			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(K8sVersionDrift))
		})
	})

	Context("Static fields", func() {
		It("should not trigger drift if NodeClass hasn't changed", func() {
			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})

		It("should trigger drift if NodeClass subnet changed", func() {
			testSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/my-subnet"
			nodeClass.Spec.VNETSubnetID = lo.ToPtr(testSubnetID)
			ExpectApplied(ctx, env.Client, nodeClass)
			ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)

			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NodeClassDrift))
		})

		It("should trigger drift if ImageFamily changed", func() {
			nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
			ExpectApplied(ctx, env.Client, nodeClass)
			ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)

			drifted, err := cloudProvider.IsDrifted(ctx, getDriftNodeClaim())
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(Equal(NodeClassDrift))
		})
	})
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = AKSMachineAPIHeaderBatch", func() {
		BeforeEach(func() { setupAKSMachineAPIMode() })
		AfterEach(func() { teardownAKSMachineAPIMode() })

		Context("Drift", func() {
			var driftNodeClaim *karpv1.NodeClaim
			var driftNode *v1.Node
			var createInput *fake.AKSMachineCreateOrUpdateInput

			BeforeEach(func() {
				instanceType := "Standard_D2_v2"
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
				})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				driftNode = ExpectScheduled(ctx, env.Client, pod)
				if nodeClass.Status.KubernetesVersion != nil {
					driftNode.Status.NodeInfo.KubeletVersion = "v" + *nodeClass.Status.KubernetesVersion
				}
				driftNode.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2"
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ProvisionMode:           lo.ToPtr(testOptions.ProvisionMode),
					UseSIG:                  lo.ToPtr(true),
					KubeletIdentityClientID: lo.ToPtr(driftNode.Labels[v1beta1.AKSLabelKubeletIdentityClientID]),
				}))

				ExpectApplied(ctx, env.Client, driftNode)

				// AKS Machine API: extract createInput for mode-specific tests
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput = azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()

				// Get the NodeClaim from the K8s API (not cloudProvider.List()) to preserve
				// annotations (e.g., hash) set by the provisioner during creation.
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))
				// Use the name from List() to fetch the full object from K8s
				driftNodeClaim = &karpv1.NodeClaim{}
				Expect(env.Client.Get(ctx, types.NamespacedName{Name: nodeClaims[0].Name}, driftNodeClaim)).To(Succeed())
				driftNodeClaim.Status.NodeName = driftNode.Name
				ExpectApplied(ctx, env.Client, driftNodeClaim)
			})

			// Shared drift tests (run under both modes)
			runSharedDriftTests(func() *karpv1.NodeClaim { return driftNodeClaim }, func() *v1.Node { return driftNode })

			// AKSMachineAPIHeaderBatch-specific drift tests
			Context("AKSMachineAPIHeaderBatch-specific", func() {
				// Mode-specific: DriftAction is a Machine API concept (drift.go:61-66); VMs detect drift client-side.
				It("should trigger drift when DriftAction field is available", func() {
					aksMachineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, createInput.AKSMachineName)

					existingMachine, ok := azureEnv.AKSDataStorage.AKSMachines.Load(aksMachineID)
					Expect(ok).To(BeTrue(), "AKS machine should exist in fake store")

					aksMachine := existingMachine
					if aksMachine.Properties == nil {
						aksMachine.Properties = &armcontainerservice.MachineProperties{}
					}
					if aksMachine.Properties.Status == nil {
						aksMachine.Properties.Status = &armcontainerservice.MachineStatus{}
					}
					aksMachine.Properties.Status.DriftAction = lo.ToPtr(armcontainerservice.DriftActionRecreate)
					aksMachine.Properties.Status.DriftReason = lo.ToPtr("ClusterConfigurationChanged")

					azureEnv.AKSDataStorage.AKSMachines.Store(aksMachineID, aksMachine)

					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ClusterConfigDrift))
				})
			})
		})
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() { setupAKSScriptlessMode() })
		AfterEach(func() { teardownAKSScriptlessMode() })

		Context("Drift", func() {
			var driftNodeClaim *karpv1.NodeClaim
			var driftNode *v1.Node

			BeforeEach(func() {
				instanceType := "Standard_D2_v2"
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
				})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				driftNode = ExpectScheduled(ctx, env.Client, pod)
				if nodeClass.Status.KubernetesVersion != nil {
					driftNode.Status.NodeInfo.KubeletVersion = "v" + *nodeClass.Status.KubernetesVersion
				}
				driftNode.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2"
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					KubeletIdentityClientID: lo.ToPtr(driftNode.Labels[v1beta1.AKSLabelKubeletIdentityClientID]),
				}))

				ExpectApplied(ctx, env.Client, driftNode)

				// AKSScriptless: extract NodeClaim via VM name
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()

				nodeClaimName := GetNodeClaimNameFromVMName(input.VMName)
				driftNodeClaim = &karpv1.NodeClaim{}
				Expect(env.Client.Get(ctx, types.NamespacedName{Name: nodeClaimName}, driftNodeClaim)).To(Succeed())
				driftNodeClaim.Status.NodeName = driftNode.Name
				ExpectApplied(ctx, env.Client, driftNodeClaim)
			})

			// Shared drift tests (run under both modes)
			runSharedDriftTests(func() *karpv1.NodeClaim { return driftNodeClaim }, func() *v1.Node { return driftNode })

			// AKSScriptless-specific drift tests
			Context("AKSScriptless-specific", func() {
				// Mode-specific: Machine API always uses SIG, so CIG→SIG transition only applies to VMs.
				It("should trigger drift when the image gallery changes to SIG", func() {
					test.ApplySIGImages(nodeClass)
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ImageDrift))
				})

				// Kubelet Client ID drift is only checked for non-AKS-machine (VM/legacy) nodes.
				// AKS Machine nodes use isMachineDrifted/DriftAction instead. See drift.go lines 60-75.
				// Mode-specific: isKubeletIdentityDrifted only runs for VM nodes (drift.go:68-74); Machine API uses DriftAction.
				Context("Kubelet Client ID", func() {
					It("should NOT trigger drift if node doesn't have kubelet client ID label", func() {
						driftNode.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = ""
						ExpectApplied(ctx, env.Client, driftNode)

						drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
						Expect(err).ToNot(HaveOccurred())
						Expect(drifted).To(BeEmpty())
					})

					It("should trigger drift if node kubelet client ID doesn't match options", func() {
						ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
							KubeletIdentityClientID: lo.ToPtr("3824ff7a-93b6-40af-b861-2eb621ba437a"), // a different random UUID
						}))

						drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
						Expect(err).ToNot(HaveOccurred())
						Expect(drifted).To(Equal(KubeletIdentityDrift))
					})
				})
			})
		})
	})
})
