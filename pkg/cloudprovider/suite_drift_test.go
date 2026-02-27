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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/awslabs/operatorpkg/object"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var _ = Describe("CloudProvider", func() {
	// Attention: tests under "ProvisionMode = AKSScriptless" are not applicable to ProvisionMode = AKSMachineAPI option.
	// Due to different assumptions, not all tests can be shared. Add tests for AKS machine instances in a different Context/file.
	// If ProvisionMode = AKSScriptless is no longer supported, their code/tests will be replaced with ProvisionMode = AKSMachineAPI.
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
			cluster.Reset()
			azureEnv.Reset()
		})

		Context("Drift", func() {
			var driftNodeClaim *karpv1.NodeClaim
			var pod *v1.Pod
			var node *v1.Node

			BeforeEach(func() {
				// Set up VM provisioning mode environment for drift testing
				testOptions = test.Options()
				ctx = coreoptions.ToContext(ctx, coretest.Options())
				ctx = options.ToContext(ctx, testOptions)
				azureEnv = test.NewEnvironment(ctx, env)
				test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
				cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
				cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
				coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

				instanceType := "Standard_D2_v2"
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod = coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
				})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node = ExpectScheduled(ctx, env.Client, pod)
				// KubeletVersion must be applied to the node to satisfy k8s drift
				if nodeClass.Status.KubernetesVersion != nil {
					node.Status.NodeInfo.KubeletVersion = "v" + *nodeClass.Status.KubernetesVersion
				}

				node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2" // random UUID
				// Context must have same kubelet client id
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					KubeletIdentityClientID: lo.ToPtr(node.Labels[v1beta1.AKSLabelKubeletIdentityClientID]),
				}))

				ExpectApplied(ctx, env.Client, node)
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				rg := input.ResourceGroupName
				vmName := input.VMName
				// Corresponding NodeClaim
				driftNodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
					Status: karpv1.NodeClaimStatus{
						NodeName: node.Name,
						// TODO (charliedmcb): switch back to use MkVMID, and update the test subscription usage to all use the same sub const 12345678-1234-1234-1234-123456789012
						//     We currently need this work around for the List nodes call to work in Drift, since the VM ID is overridden here (which uses the sub id in the instance provider):
						//     https://github.com/Azure/karpenter-provider-azure/blob/84e449787ec72268efb0c7af81ec87a6b3ee95fa/pkg/providers/instance/instance.go#L604
						//     which has the sub const 12345678-1234-1234-1234-123456789012 passed in here:
						//     https://github.com/Azure/karpenter-provider-azure/blob/84e449787ec72268efb0c7af81ec87a6b3ee95fa/pkg/test/environment.go#L152
						ProviderID: utils.VMResourceIDToProviderID(ctx, fmt.Sprintf("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", rg, vmName)),
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							karpv1.NodePoolLabelKey:    nodePool.Name,
							v1.LabelInstanceTypeStable: instanceType,
						},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
							Name:  nodeClass.Name,
						},
					},
				})
			})

			It("should not fail if nodeClass does not exist", func() {
				ExpectDeleted(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should not fail if nodePool does not exist", func() {
				ExpectDeleted(ctx, env.Client, nodePool)
				drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should not return drifted if the NodeClaim is valid", func() {
				drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should error drift if NodeClaim doesn't have provider id", func() {
				driftNodeClaim.Status = karpv1.NodeClaimStatus{}
				drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			Context("Node Image Drift", func() {
				It("should succeed with no drift when nothing changes", func() {
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should succeed with no drift when ConditionTypeImagesReady is not true", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNoLongerReady", "test when images aren't ready")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				// Note: this case shouldn't be able to happen in practice since if Images is empty ConditionTypeImagesReady should be false.
				It("should error when Images are empty", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.Status.Images = []v1beta1.NodeImage{}
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).To(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should trigger drift when the image gallery changes to SIG", func() {
					test.ApplySIGImages(nodeClass)
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ImageDrift))
				})

				It("should trigger drift when the image version changes", func() {
					test.ApplyCIGImagesWithVersion(nodeClass, "202503.02.0")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ImageDrift))
				})
			})

			Context("Kubernetes Version", func() {
				It("should succeed with no drift when nothing changes", func() {
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should succeed with no drift when KubernetesVersionReady is not true", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "K8sVersionNoLongerReady", "test when k8s isn't ready")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
				It("shouldn't error or be drifted when KubernetesVersion is empty", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.Status.KubernetesVersion = to.Ptr("")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when NodeName is missing", func() {
					driftNodeClaim.Status.NodeName = ""
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when node is not found", func() {
					driftNodeClaim.Status.NodeName = "NodeWhoDoesNotExist"
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when node is deleting", func() {
					node = ExpectNodeExists(ctx, env.Client, driftNodeClaim.Status.NodeName)
					node.Finalizers = append(node.Finalizers, test.TestingFinalizer)
					ExpectApplied(ctx, env.Client, node)
					Expect(env.Client.Delete(ctx, node)).ToNot(HaveOccurred())
					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))

					// cleanup
					node = ExpectNodeExists(ctx, env.Client, driftNodeClaim.Status.NodeName)
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
					nodeClass.Status.KubernetesVersion = to.Ptr(semverCurrentK8sVersion.String())

					ExpectApplied(ctx, env.Client, nodeClass)

					drifted, err := cloudProvider.IsDrifted(ctx, driftNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(K8sVersionDrift))
				})
			})

			Context("Kubelet Client ID", func() {
				It("should NOT trigger drift if node doesn't have kubelet client ID label", func() {
					node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "" // Not set

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
