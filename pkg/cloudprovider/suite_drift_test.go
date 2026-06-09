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
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/awslabs/operatorpkg/object"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

func runDriftTests(provisionMode provisionModeTestCase) {
	Context("Drift", func() {
		var nodeClaim *karpv1.NodeClaim
		var providerInstanceName string

		BeforeEach(func() {
			instanceType := "Standard_D2_v2"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
			})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			if nodeClass.Status.KubernetesVersion != nil {
				node.Status.NodeInfo.KubeletVersion = "v" + *nodeClass.Status.KubernetesVersion
			}
			node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2"

			opts := *options.FromContext(ctx)
			opts.KubeletIdentityClientID = node.Labels[v1beta1.AKSLabelKubeletIdentityClientID]
			ctx = options.ToContext(ctx, &opts)

			ExpectApplied(ctx, env.Client, node)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				providerInstanceName = lo.FromPtr(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine.Name)
			} else {
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				providerInstanceName = lo.FromPtr(&azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName)
			}

			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))

			listedNodeClaim := nodeClaims[0]
			Expect(node.Spec.ProviderID).ToNot(BeEmpty())
			nodeClaim = &karpv1.NodeClaim{}
			Expect(env.Client.Get(ctx, types.NamespacedName{Name: listedNodeClaim.Name}, nodeClaim)).To(Succeed())
			nodeClaim.Status = listedNodeClaim.Status
			nodeClaim.Status.ProviderID = node.Spec.ProviderID
			nodeClaim.Status.NodeName = node.Name
			nodeClaim.Spec.NodeClassRef = &karpv1.NodeClassReference{
				Group: object.GVK(nodeClass).Group,
				Kind:  object.GVK(nodeClass).Kind,
				Name:  nodeClass.Name,
			}
			ExpectApplied(ctx, env.Client, nodeClaim)
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

			// Empty Images should normally make ImagesReady false before drift reaches this branch.
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

			// Machine API mode never support CIG
			if !provisionMode.isAKSMachineMode() {
				It("should trigger drift when the image gallery changes to SIG", func() {
					test.ApplySIGImages(nodeClass)
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ImageDrift))
				})
			}
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
				nodeClass.Status.KubernetesVersion = lo.ToPtr("")
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
				node := ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
				node.Finalizers = append(node.Finalizers, test.TestingFinalizer)
				ExpectApplied(ctx, env.Client, node)
				Expect(env.Client.Delete(ctx, node)).ToNot(HaveOccurred())
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))

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

				semverCurrentK8sVersion := lo.Must(semver.ParseTolerant(*nodeClass.Status.KubernetesVersion))
				semverCurrentK8sVersion.Minor = semverCurrentK8sVersion.Minor + 1
				nodeClass.Status.KubernetesVersion = lo.ToPtr(semverCurrentK8sVersion.String())

				ExpectApplied(ctx, env.Client, nodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(K8sVersionDrift))
			})
		})

		Context("Static fields", func() {
			It("should trigger drift if NodeClass subnet changed", func() {
				testSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/my-subnet"
				nodeClass.Spec.VNETSubnetID = lo.ToPtr(testSubnetID)
				ExpectApplied(ctx, env.Client, nodeClass)
				ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NodeClassDrift))
			})

			It("should trigger drift if ImageFamily changed", func() {
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				ExpectApplied(ctx, env.Client, nodeClass)
				ExpectNodeClassHashUpdated(ctx, env.Client, nodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NodeClassDrift))
			})
		})

		// DriftAction is Machine API specific design.
		if provisionMode.isAKSMachineMode() {
			Context("AKS Machine DriftAction", func() {
				It("should trigger drift when DriftAction field is available", func() {
					aksMachineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, providerInstanceName)
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

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ClusterConfigDrift))
				})
			})
		}

		// For Machine API modes, Kubelet Client ID drift is handled by Machine API.
		if !provisionMode.isAKSMachineMode() {
			Context("Kubelet Client ID", func() {
				It("should NOT trigger drift if node doesn't have kubelet client ID label", func() {
					node := ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
					node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = ""
					ExpectApplied(ctx, env.Client, node)

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(BeEmpty())
				})

				It("should trigger drift if node kubelet client ID doesn't match options", func() {
					opts := *options.FromContext(ctx)
					opts.KubeletIdentityClientID = "3824ff7a-93b6-40af-b861-2eb621ba437a"
					ctx = options.ToContext(ctx, &opts)

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(KubeletIdentityDrift))
				})
			})
		}
	})
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = AKSMachineAPIHeaderBatch", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPIHeaderBatch),
				UseSIG:        lo.ToPtr(true),
			})

			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnvNonZonal.InstanceTypeStore)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
			coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, recorder, cloudProviderNonZonal, clusterNonZonal, fakeClock)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset(ctx)
			azureEnvNonZonal.Reset(ctx)
		})

		runDriftTests(aksMachineAPIHeaderBatchProvisionMode())
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSScriptless),
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset(ctx)
		})

		runDriftTests(aksscriptlessProvisionMode())
	})
})
