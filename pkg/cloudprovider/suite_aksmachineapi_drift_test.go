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
	"github.com/awslabs/operatorpkg/object"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = AKSMachineAPI", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				UseSIG:        lo.ToPtr(true),
			})

			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider)
			cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
			coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
		})

		AfterEach(func() {
			cluster.Reset()
			azureEnv.Reset()
			azureEnvNonZonal.Reset()
		})

		Context("Drift", func() {
			var nodeClaim *karpv1.NodeClaim
			var node *v1.Node
			var createInput *fake.AKSMachineCreateOrUpdateInput

			BeforeEach(func() {
				instanceType := "Standard_D2_v2"
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
				})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node = ExpectScheduled(ctx, env.Client, pod)
				// KubeletVersion must be applied to the node to satisfy k8s drift
				node.Status.NodeInfo.KubeletVersion = "v" + nodeClass.Status.KubernetesVersion
				node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2" // random UUID

				ExpectApplied(ctx, env.Client, node)
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput = azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()

				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))

				nodeClaim = nodeClaims[0]
				nodeClaim.Status.NodeName = node.Name // Normally core would do this.
				nodeClaim.Spec.NodeClassRef = &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				}
			})

			It("should not fail if nodeClass does not exist", func() {
				ExpectDeleted(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should not fail if nodePool does not exist", func() {
				ExpectDeleted(ctx, env.Client, nodePool)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should not return drifted if the NodeClaim is valid", func() {
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should error drift if NodeClaim doesn't have provider id", func() {
				nodeClaim.Status = karpv1.NodeClaimStatus{}
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			Context("Node Image Drift", func() {
				It("should trigger drift when DriftAction field is available", func() {
					// Find the AKS machine that was created during BeforeEach
					aksMachineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, createInput.AKSMachineName)

					// Get the existing machine from the fake store
					existingMachine, ok := azureEnv.AKSDataStorage.AKSMachines.Load(aksMachineID)
					Expect(ok).To(BeTrue(), "AKS machine should exist in fake store")

					aksMachine := existingMachine.(armcontainerservice.Machine)

					// Set DriftAction to "Recreate" to trigger drift
					if aksMachine.Properties == nil {
						aksMachine.Properties = &armcontainerservice.MachineProperties{}
					}
					if aksMachine.Properties.Status == nil {
						aksMachine.Properties.Status = &armcontainerservice.MachineStatus{}
					}
					aksMachine.Properties.Status.DriftAction = lo.ToPtr(armcontainerservice.DriftActionRecreate)
					aksMachine.Properties.Status.DriftReason = lo.ToPtr("ClusterConfigurationChanged")

					// Update the machine in the fake store
					azureEnv.AKSDataStorage.AKSMachines.Store(aksMachineID, aksMachine)

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ClusterConfigDrift))
				})
			})

			Context("Node Image Drift", func() {
				It("should succeed with no drift when nothing changes", func() {
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should succeed with no drift when ConditionTypeImagesReady is not true", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNoLongerReady", "test when images aren't ready")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				// Note: this case shouldn't be able to happen in practice since if Images is empty ConditionTypeImagesReady should be false.
				It("should error when Images are empty", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.Status.Images = []v1beta1.NodeImage{}
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).To(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should trigger drift when the image version changes", func() {
					test.ApplyCIGImagesWithVersion(nodeClass, "202503.02.0")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ImageDrift))
				})
			})

			Context("Kubernetes Version", func() {
				It("should succeed with no drift when nothing changes", func() {
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should succeed with no drift when KubernetesVersionReady is not true", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "K8sVersionNoLongerReady", "test when k8s isn't ready")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
				It("shouldn't error or be drifted when KubernetesVersion is empty", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.Status.KubernetesVersion = ""
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when NodeName is missing", func() {
					nodeClaim.Status.NodeName = ""
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when node is not found", func() {
					nodeClaim.Status.NodeName = "NodeWhoDoesNotExist"
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when node is deleting", func() {
					node = ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
					node.Finalizers = append(node.Finalizers, test.TestingFinalizer)
					ExpectApplied(ctx, env.Client, node)
					Expect(env.Client.Delete(ctx, node)).ToNot(HaveOccurred())
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))

					// cleanup
					node = ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
					deepCopy := node.DeepCopy()
					node.Finalizers = lo.Reject(node.Finalizers, func(finalizer string, _ int) bool {
						return finalizer == test.TestingFinalizer
					})
					Expect(env.Client.Patch(ctx, node, client.StrategicMergeFrom(deepCopy))).NotTo(HaveOccurred())
					ExpectDeleted(ctx, env.Client, node)
				})

				It("should succeed with drift true when KubernetesVersion is new", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)

					semverCurrentK8sVersion := lo.Must(semver.ParseTolerant(nodeClass.Status.KubernetesVersion))
					semverCurrentK8sVersion.Minor = semverCurrentK8sVersion.Minor + 1
					nodeClass.Status.KubernetesVersion = semverCurrentK8sVersion.String()

					ExpectApplied(ctx, env.Client, nodeClass)

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(K8sVersionDrift))
				})
			})
		})
	})
})
