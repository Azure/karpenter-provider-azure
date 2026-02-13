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
	"time"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var _ = Describe("CloudProvider - Features", func() {

	// === SHARED TEST FUNCTIONS ===
	// These run for both AKSMachineAPI and AKSScriptless (VM) modes.

	runSharedImageSelectionTests := func(mode provisionTestMode) {
		Context("Create - Image Selection (SIG)", func() {
			It("should use shared image gallery images", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.imageRef).To(ContainSubstring("AKSUbuntu"))

				// VM mode: verify SIG is used (not community gallery) and subscription ID is embedded
				if mode.isVM {
					Expect(result.isCommunityGalleryImage).To(BeFalse())
					Expect(result.imageRef).To(ContainSubstring(options.FromContext(ctx).SIGSubscriptionID))
				}
			})

			DescribeTable("should select the right SIG image for Ubuntu instance types",
				func(instanceType string, expectedImageDefinition string) {
					nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
					coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{instanceType},
						}})

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(mode.getCreateCallCount()).To(Equal(1))
					result := mode.popCreationResult()
					Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))

					// VM mode: verify full SIG gallery prefix in image reference
					if mode.isVM {
						expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s",
							options.FromContext(ctx).SIGSubscriptionID, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName, expectedImageDefinition)
						Expect(result.imageRef).To(ContainSubstring(expectedPrefix))
					}
				},
				Entry("Gen2 instance type", "Standard_D2_v5", imagefamily.Ubuntu2204Gen2ImageDefinition),
				Entry("Gen1 instance type", "Standard_D2_v3", imagefamily.Ubuntu2204Gen1ImageDefinition),
				Entry("ARM instance type", "Standard_D16plds_v5", imagefamily.Ubuntu2204Gen2ArmImageDefinition),
			)

			It("should select the right SIG image for Gen2 instance type with AzureLinux", func() {
				instanceType := "Standard_D2_v5"
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))

				// VM mode: verify full AzureLinux SIG gallery prefix
				if mode.isVM {
					expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s",
						options.FromContext(ctx).SIGSubscriptionID, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName, expectedImageDefinition)
					Expect(result.imageRef).To(ContainSubstring(expectedPrefix))
				}
			})

			It("should select the right SIG image for Gen1 instance type with AzureLinux", func() {
				instanceType := "Standard_D2_v3"
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))

				// VM mode: verify full AzureLinux SIG gallery prefix
				if mode.isVM {
					expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s",
						options.FromContext(ctx).SIGSubscriptionID, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName, expectedImageDefinition)
					Expect(result.imageRef).To(ContainSubstring(expectedPrefix))
				}
			})

			It("should select the right SIG image for ARM instance type with AzureLinux", func() {
				instanceType := "Standard_D16plds_v5"
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))

				// VM mode: verify full AzureLinux SIG gallery prefix
				if mode.isVM {
					expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s",
						options.FromContext(ctx).SIGSubscriptionID, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName, expectedImageDefinition)
					Expect(result.imageRef).To(ContainSubstring(expectedPrefix))
				}
			})
		})
	}

	runSharedGPUTests := func(mode provisionTestMode) {
		Context("Create - GPU Workloads + Nodes", func() {
			It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(utils.IsNvidiaEnabledSKU(result.vmSize)).To(BeFalse())
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "0"))
			})

			It("should schedule GPU pod on GPU capable node", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Name: "samples-tf-mnist-demo",
						Labels: map[string]string{
							"app": "samples-tf-mnist-demo",
						},
					},
					Image: "mcr.microsoft.com/azuredocs/samples-tf-mnist-demo:gpu",
					ResourceRequirements: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
					RestartPolicy: v1.RestartPolicy("OnFailure"),
					Tolerations: []v1.Toleration{
						{
							Key:      "sku",
							Operator: v1.TolerationOpEqual,
							Value:    "gpu",
							Effect:   v1.TaintEffectNoSchedule,
						},
					},
				})

				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				// the following checks assume Standard_NC16as_T4_v3 (surprisingly the cheapest GPU in the test set), so test the assumption
				Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_NC16as_T4_v3"))

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(utils.IsNvidiaEnabledSKU(result.vmSize)).To(BeTrue())

				// Verify that the node the pod was scheduled on has GPU resource and labels set
				Expect(node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-name", "T4"))
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-manufacturer", v1beta1.ManufacturerNvidia))
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "1"))

				// VM mode: also verify GPU-related settings in customData/bootstrap
				if mode.isVM {
					Expect(result.customData).ToNot(BeEmpty())
					Expect(result.customData).To(SatisfyAll(
						ContainSubstring("GPU_NODE=true"),
						ContainSubstring("SGX_NODE=false"),
						ContainSubstring("MIG_NODE=false"),
						ContainSubstring("CONFIG_GPU_DRIVER_IF_NEEDED=true"),
						ContainSubstring("ENABLE_GPU_DEVICE_PLUGIN_IF_NEEDED=false"),
						ContainSubstring("GPU_DRIVER_TYPE=\"cuda\""),
						ContainSubstring(fmt.Sprintf("GPU_DRIVER_VERSION=\"%s\"", utils.NvidiaCudaDriverVersion)),
						ContainSubstring(fmt.Sprintf("GPU_IMAGE_SHA=\"%s\"", utils.AKSGPUCudaVersionSuffix)),
						ContainSubstring("GPU_NEEDS_FABRIC_MANAGER=\"false\""),
						ContainSubstring("GPU_INSTANCE_PROFILE=\"\""),
					))
				}
			})
		})
	}

	runSharedEphemeralDiskTests := func(mode provisionTestMode) {
		Context("Create - Ephemeral Disk", func() {
			It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D64s_v3"}, // Has large cache disk space
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.isEphemeral).To(BeTrue())
				Expect(result.diskSizeGB).ToNot(BeNil())
				Expect(*result.diskSizeGB).To(Equal(int32(128))) // Default 128GB minimum

				// VM mode: verify DiffDiskSettings.Option is Local
				if mode.isVM {
					Expect(result.diffDiskOption).To(Equal(string(armcompute.DiffDiskOptionsLocal)))
				}
			})

			It("should fail to provision if ephemeral disk ask for is too large", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
						Operator: v1.NodeSelectorOpGt,
						Values:   []string{"100000"},
					},
				}) // No InstanceType will match this requirement
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
			})

			It("should select an ephemeral disk if LabelSKUStorageEphemeralOSMaxSize is set and os disk size fits", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
						Operator: v1.NodeSelectorOpGt,
						Values:   []string{"0"},
					},
				})
				nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](30)

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.isEphemeral).To(BeTrue())
				Expect(result.diskSizeGB).ToNot(BeNil())
				Expect(*result.diskSizeGB).To(Equal(int32(30)))

				// VM mode: verify DiffDiskSettings.Option is Local
				if mode.isVM {
					Expect(result.diffDiskOption).To(Equal(string(armcompute.DiffDiskOptionsLocal)))
				}
			})

			It("should use ephemeral disk if supported, and set disk size to OSDiskSizeGB from node class", func() {
				nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(256))
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D64s_v3"},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.isEphemeral).To(BeTrue())
				Expect(result.diskSizeGB).ToNot(BeNil())
				Expect(*result.diskSizeGB).To(Equal(int32(256)))

				// VM mode: verify DiffDiskSettings.Option is Local
				if mode.isVM {
					Expect(result.diffDiskOption).To(Equal(string(armcompute.DiffDiskOptionsLocal)))
				}
			})

			It("should not use ephemeral disk if ephemeral is supported, but we don't have enough space", func() {
				// Standard_D2s_V3 has 53GB Of CacheDisk space and 16GB of Temp Disk Space.
				// With the rule of 128GB being the minimum OSDiskSize, this should fall back to managed disk
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2s_v3"},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.isEphemeral).To(BeFalse())
				Expect(result.diskSizeGB).ToNot(BeNil())
				Expect(*result.diskSizeGB).To(Equal(int32(128))) // Default size
				// AKS Machine mode: verify OSDiskType is explicitly Managed (preserved from original)
				if !mode.isVM {
					Expect(result.osDiskType).To(Equal("Managed"))
				}
			})
		})
	}

	runSharedAdditionalTagsTests := func(mode provisionTestMode) {
		Context("Create - Additional Tags", func() {
			It("should add additional tags to the created resource", func() {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ProvisionMode: lo.Ternary(!mode.isVM, lo.ToPtr(consts.ProvisionModeAKSMachineAPI), nil),
					UseSIG:        lo.ToPtr(true),
					AdditionalTags: map[string]string{
						"karpenter.azure.com/test-tag": "test-value",
					},
				}))

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()

				// Common tag assertions — both modes should have these
				Expect(result.tags).To(HaveKey("karpenter.azure.com_test-tag"))
				Expect(*result.tags["karpenter.azure.com_test-tag"]).To(Equal("test-value"))
				Expect(result.tags).To(HaveKey("karpenter.azure.com_cluster"))
				Expect(*result.tags["karpenter.azure.com_cluster"]).To(Equal("test-cluster"))
				Expect(result.tags).To(HaveKey("compute.aks.billing"))
				Expect(*result.tags["compute.aks.billing"]).To(Equal("linux"))
				Expect(result.tags).To(HaveKey("karpenter.sh_nodepool"))
				Expect(*result.tags["karpenter.sh_nodepool"]).To(Equal(nodePool.Name))

				if mode.isVM {
					// VM mode: verify strict tag map equality (no unexpected tags)
					Expect(result.tags).To(Equal(map[string]*string{
						"karpenter.azure.com_test-tag": lo.ToPtr("test-value"),
						"karpenter.azure.com_cluster":  lo.ToPtr("test-cluster"),
						"compute.aks.billing":          lo.ToPtr("linux"),
						"karpenter.sh_nodepool":        lo.ToPtr(nodePool.Name),
					}))

					// VM mode: NIC should also have the same tags with strict equality
					nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
					Expect(nic).NotTo(BeNil())
					Expect(nic.Interface.Tags).To(Equal(map[string]*string{
						"karpenter.azure.com_test-tag": lo.ToPtr("test-value"),
						"karpenter.azure.com_cluster":  lo.ToPtr("test-cluster"),
						"compute.aks.billing":          lo.ToPtr("linux"),
						"karpenter.sh_nodepool":        lo.ToPtr(nodePool.Name),
					}))
				} else {
					// AKS Machine mode: additional AKS-managed tags
					Expect(result.tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
					Expect(result.tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
				}
			})
		})
	}

	runSharedSubnetTests := func(mode provisionTestMode) {
		Context("Create - Subnet Selection", func() {
			It("should use the subnet specified in the nodeclass", func() {
				// BYO VNet pattern: the subnet reconciler requires custom subnets to be in the
				// same non-managed VNet as the cluster subnet. Both VM and AKS Machine modes
				// need this because setupVMMode/setupAKSMachineAPIMode call ExpectObjectReconciled.
				byoClusterSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/cluster-subnet"
				testSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/nodeclassSubnet"

				var byoOpts *options.Options
				if mode.isVM {
					byoOpts = test.Options(test.OptionsFields{
						UseSIG:   lo.ToPtr(true),
						SubnetID: lo.ToPtr(byoClusterSubnetID),
					})
				} else {
					byoOpts = test.Options(test.OptionsFields{
						ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
						UseSIG:        lo.ToPtr(true),
						SubnetID:      lo.ToPtr(byoClusterSubnetID),
					})
					byoOpts.BatchCreationEnabled = true
					byoOpts.BatchIdleTimeoutMS = 100
					byoOpts.BatchMaxTimeoutMS = 1000
					byoOpts.MaxBatchSize = 50
				}
				byoCtx := options.ToContext(ctx, byoOpts)

				nodeClass.Spec.VNETSubnetID = lo.ToPtr(testSubnetID)
				ExpectApplied(byoCtx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(byoCtx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(byoCtx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(byoCtx, env.Client, pod)
				Expect(mode.getSubnetID()).To(Equal(testSubnetID))
			})
		})
	}

	runSharedKubeletConfigTests := func(mode provisionTestMode) {
		Context("Create - KubeletConfig", func() {
			It("should support provisioning with kubeletConfig", func() {
				// Use mode-specific GC thresholds to match original tests:
				// VM: 30/20 (original VM kubelet test), AKS Machine: 85/80 (original AKS Machine test)
				gcHigh := int32(30)
				gcLow := int32(20)
				if !mode.isVM {
					gcHigh = int32(85)
					gcLow = int32(80)
				}

				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            "static",
					CPUCFSQuota:                 lo.ToPtr(true),
					CPUCFSQuotaPeriod:           metav1.Duration{},
					ImageGCHighThresholdPercent: lo.ToPtr(gcHigh),
					ImageGCLowThresholdPercent:  lo.ToPtr(gcLow),
					TopologyManagerPolicy:       "best-effort",
					AllowedUnsafeSysctls:        []string{"Allowed", "Unsafe", "Sysctls"},
					ContainerLogMaxSize:         "42Mi",
					ContainerLogMaxFiles:        lo.ToPtr[int32](13),
					PodPidsLimit:                lo.ToPtr[int64](99),
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				if mode.isVM {
					// VM: verify kubelet flags are passed through in customData
					customData := ExpectDecodedCustomData(azureEnv)
					expectedFlags := map[string]string{
						"eviction-hard":           "memory.available<750Mi",
						"image-gc-high-threshold": fmt.Sprintf("%d", gcHigh),
						"image-gc-low-threshold":  fmt.Sprintf("%d", gcLow),
						"cpu-cfs-quota":           "true",
						"max-pods":                "250",
						"topology-manager-policy": "best-effort",
						"container-log-max-size":  "42Mi",
						"allowed-unsafe-sysctls":  "Allowed,Unsafe,Sysctls",
						"cpu-manager-policy":      "static",
						"container-log-max-files": "13",
						"pod-max-pids":            "99",
					}
					ExpectKubeletFlags(azureEnv, customData, expectedFlags)
					Expect(customData).To(SatisfyAny(
						ContainSubstring("--system-reserved=cpu=0,memory=0"),
						ContainSubstring("--system-reserved=memory=0,cpu=0"),
					))
					Expect(customData).To(SatisfyAny(
						ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
						ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
					))
				} else {
					// AKS Machine: verify typed kubelet config properties on the AKS machine
					Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
					aksMachine := input.AKSMachine
					Expect(aksMachine.Properties.Kubernetes.KubeletConfig).ToNot(BeNil())
					Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.CPUManagerPolicy).To(Equal("static"))
					Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.CPUCfsQuota).To(Equal(true))
					Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.ImageGcHighThreshold).To(Equal(gcHigh))
					Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.ImageGcLowThreshold).To(Equal(gcLow))
				}
			})
		})
	}

	runSharedReuseExistingResourceTests := func(mode provisionTestMode) {
		Context("Create - Reuse Existing Resource", func() {
			It("should not reattempt creation of a resource that has been created before", func() {
				if mode.isVM {
					// VM mode: pre-store a VM in the fake store, then verify CreateAndDrain succeeds
					// by finding it via GET instead of creating a new one
					testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"karpenter.sh/nodepool": nodePool.Name},
						},
						Spec: karpv1.NodeClaimSpec{NodeClassRef: &karpv1.NodeClassReference{Name: nodeClass.Name}},
					})
					vmName := instance.GenerateResourceName(testNodeClaim.Name)
					vm := &armcompute.VirtualMachine{
						Name:     lo.ToPtr(vmName),
						ID:       lo.ToPtr(fake.MkVMID(options.FromContext(ctx).NodeResourceGroup, vmName)),
						Location: lo.ToPtr(fake.Region),
						Zones:    []*string{lo.ToPtr("fantasy-zone")},
						Properties: &armcompute.VirtualMachineProperties{
							TimeCreated: lo.ToPtr(time.Now()),
							HardwareProfile: &armcompute.HardwareProfile{
								VMSize: lo.ToPtr(armcompute.VirtualMachineSizeTypesBasicA3),
							},
						},
					}
					azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					_, err := CreateAndDrain(ctx, cloudProvider, azureEnv, testNodeClaim)
					Expect(err).ToNot(HaveOccurred())
				} else {
					// AKS Machine mode: create first, then create again with deep-copied claim and
					// verify the machine was reused (GET, not CreateOrUpdate)
					firstNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
						},
						Spec: karpv1.NodeClaimSpec{
							NodeClassRef: &karpv1.NodeClassReference{
								Group: object.GVK(nodeClass).Group,
								Kind:  object.GVK(nodeClass).Kind,
								Name:  nodeClass.Name,
							},
							Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
								{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")}}},
								{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{"Standard_D2_v2"}}},
							},
						},
					})
					ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
					createdFirst, err := CreateAndDrain(ctx, cloudProvider, azureEnv, firstNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(createdFirst).ToNot(BeNil())

					// Create again with the same claim — should reuse
					conflictedNodeClaim := firstNodeClaim.DeepCopy()
					azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
					azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
					reusedClaim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(reusedClaim).ToNot(BeNil())

					// Verify reuse: no new CreateOrUpdate, just a Get
					Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
					Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
				}
			})
		})
	}

	// === MODE CONTEXTS ===

	Context("ProvisionMode = AKSMachineAPI", func() {
		BeforeEach(func() { setupAKSMachineAPIMode() })
		AfterEach(func() { teardownProvisionMode() })

		mode := aksMachineProvisionMode()
		runSharedImageSelectionTests(mode)
		runSharedGPUTests(mode)
		runSharedEphemeralDiskTests(mode)
		runSharedAdditionalTagsTests(mode)
		runSharedSubnetTests(mode)
		runSharedKubeletConfigTests(mode)
		runSharedReuseExistingResourceTests(mode)

		// === AKS Machine API Only Tests ===

		Context("Create - Additional Configurations", func() {
			It("should handle configured NodeClass", func() {
				// Comprehensive integration test: exercises kubelet config + image family + BYO VNet subnet +
				// custom tags + Karpenter tags + OS disk size + GPU selection + image version, all at once.
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            "static",
					CPUCFSQuota:                 lo.ToPtr(true),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
				}
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)

				// Override context to use a BYO VNet instead of managed VNet
				byoClusterSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/cluster-subnet"
				byoOpts := test.Options(test.OptionsFields{
					ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
					UseSIG:        lo.ToPtr(true),
					SubnetID:      lo.ToPtr(byoClusterSubnetID),
				})
				byoOpts.BatchCreationEnabled = true
				byoOpts.BatchIdleTimeoutMS = 100
				byoOpts.BatchMaxTimeoutMS = 1000
				byoOpts.MaxBatchSize = 50
				byoCtx := options.ToContext(ctx, byoOpts)

				// Extract cluster subnet components and create a test subnet in the same VNet
				clusterSubnetComponents, err := utils.GetVnetSubnetIDComponents(byoClusterSubnetID)
				Expect(err).ToNot(HaveOccurred())
				testSubnetID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/nodeclass-subnet",
					clusterSubnetComponents.SubscriptionID, clusterSubnetComponents.ResourceGroupName, clusterSubnetComponents.VNetName)
				nodeClass.Spec.VNETSubnetID = lo.ToPtr(testSubnetID)
				nodeClass.Spec.Tags = map[string]string{
					"custom-tag":  "custom-value",
					"environment": "test",
					"team":        "platform",
				}
				nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(100))

				// Configure GPU workload to test GPU node selection
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
						Limits: v1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				})

				ExpectApplied(byoCtx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(byoCtx, env.Client, statusController, nodeClass)
				ExpectProvisionedAndDrained(byoCtx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(byoCtx, env.Client, pod)

				// Verify AKS machine was created
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := input.AKSMachine

				// Verify kubelet configuration
				Expect(aksMachine.Properties.Kubernetes.KubeletConfig).ToNot(BeNil())
				Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.CPUManagerPolicy).To(Equal("static"))
				Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.CPUCfsQuota).To(Equal(true))
				Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.ImageGcHighThreshold).To(Equal(int32(85)))
				Expect(*aksMachine.Properties.Kubernetes.KubeletConfig.ImageGcLowThreshold).To(Equal(int32(80)))

				// Verify image family configuration
				Expect(string(*aksMachine.Properties.OperatingSystem.OSSKU)).To(Equal(v1beta1.Ubuntu2204ImageFamily))

				// Verify subnet configuration
				Expect(aksMachine.Properties.Network).ToNot(BeNil())
				Expect(aksMachine.Properties.Network.VnetSubnetID).ToNot(BeNil())
				Expect(*aksMachine.Properties.Network.VnetSubnetID).To(Equal(testSubnetID))

				// Verify custom tags from NodeClass
				Expect(aksMachine.Properties.Tags).To(HaveKey("custom-tag"))
				Expect(*aksMachine.Properties.Tags["custom-tag"]).To(Equal("custom-value"))
				Expect(aksMachine.Properties.Tags).To(HaveKey("environment"))
				Expect(*aksMachine.Properties.Tags["environment"]).To(Equal("test"))
				Expect(aksMachine.Properties.Tags).To(HaveKey("team"))
				Expect(*aksMachine.Properties.Tags["team"]).To(Equal("platform"))

				// Verify Karpenter-managed tags are still present and correct
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.sh_nodepool"))
				Expect(aksMachine.Properties.Tags["karpenter.sh_nodepool"]).To(Equal(&nodePool.Name))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
				Expect(aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(&testOptions.ClusterName))
				Expect(aksMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))

				// Verify OS disk size configuration
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(100)))

				// Verify GPU node was selected
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				vmSize := *aksMachine.Properties.Hardware.VMSize
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeTrue())

				// Verify image selection - NodeImageVersion should be set correctly
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())
				Expect(*aksMachine.Properties.NodeImageVersion).To(MatchRegexp(`^AKSUbuntu-.*-.*$`))
			})

			It("should handle configured NodeClaim", func() {
				nodeClaim.Spec.Taints = []v1.Taint{
					{Key: "test-taint", Value: "test-value", Effect: v1.TaintEffectNoSchedule},
				}
				nodeClaim.Spec.StartupTaints = []v1.Taint{
					{Key: "startup-taint", Value: "startup-value", Effect: v1.TaintEffectNoExecute},
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
				_, err := CreateAndDrain(ctx, cloudProvider, azureEnv, nodeClaim)
				Expect(err).ToNot(HaveOccurred())

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				machine := input.AKSMachine

				Expect(machine.Properties.Kubernetes.NodeInitializationTaints).To(ContainElement(lo.ToPtr("test-taint=test-value:NoSchedule")))
				Expect(machine.Properties.Kubernetes.NodeInitializationTaints).To(ContainElement(lo.ToPtr("startup-taint=startup-value:NoExecute")))
			})

			It("should not allow the user to override Karpenter-managed tags", func() {
				nodeClass.Spec.Tags = map[string]string{
					"karpenter.azure.com/cluster": "my-override-cluster",
					"karpenter.sh/nodepool":       "my-override-nodepool",
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := input.AKSMachine

				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.sh_nodepool"))
				Expect(aksMachine.Properties.Tags["karpenter.sh_nodepool"]).To(Equal(&nodePool.Name))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
				Expect(aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(&testOptions.ClusterName))

				Expect(*aksMachine.Properties.Tags["karpenter.sh_nodepool"]).ToNot(Equal("my-override-nodepool"))
				Expect(*aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).ToNot(Equal("my-override-cluster"))
			})
		})

		Context("Create - EncryptionAtHost", func() {
			It("should create AKS machine with EncryptionAtHost enabled when specified in AKSNodeClass", func() {
				if nodeClass.Spec.Security == nil {
					nodeClass.Spec.Security = &v1beta1.Security{}
				}
				nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(true)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.Security).ToNot(BeNil())
				Expect(aksMachine.Properties.Security.EnableEncryptionAtHost).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Security.EnableEncryptionAtHost)).To(BeTrue())
			})

			It("should create AKS machine with EncryptionAtHost disabled when specified in AKSNodeClass", func() {
				if nodeClass.Spec.Security == nil {
					nodeClass.Spec.Security = &v1beta1.Security{}
				}
				nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(false)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.Security).ToNot(BeNil())
				Expect(aksMachine.Properties.Security.EnableEncryptionAtHost).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Security.EnableEncryptionAtHost)).To(BeFalse())
			})

			It("should create AKS machine with EncryptionAtHost disabled when not specified in AKSNodeClass", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.Security).ToNot(BeNil())
				Expect(aksMachine.Properties.Security.EnableEncryptionAtHost).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Security.EnableEncryptionAtHost)).To(BeFalse())
			})
		})
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() { setupVMMode() })
		AfterEach(func() { teardownProvisionMode() })

		mode := vmProvisionMode()
		runSharedImageSelectionTests(mode)
		runSharedGPUTests(mode)
		runSharedEphemeralDiskTests(mode)
		runSharedAdditionalTagsTests(mode)
		runSharedSubnetTests(mode)
		runSharedKubeletConfigTests(mode)
		runSharedReuseExistingResourceTests(mode)
	})
})
