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

// suite_features_test.go tests instance configuration features:
// image selection, GPU workloads, ephemeral disk, additional tags, subnet selection,
// KubeletConfig, EncryptionAtHost, LinuxOSConfig, ArtifactStreaming, LocalDNS, etc.
//
// Each provision mode has its own Context. Within each Context:
//   - Shared tests (run under all modes) go at the top level or in shared functions.
//   - Mode-specific tests go in a clearly labeled sub-Context (e.g., "AKSScriptless-specific").
//
// For instance selection and error handling, see suite_offerings_test.go.
// For lifecycle/CRUD operations, see suite_integration_test.go.
// For drift detection, see suite_drift_test.go.

import (
	"fmt"


	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// runSharedEphemeralDiskTests tests ephemeral disk selection behavior across provision modes.
// The core behavior is the same: select ephemeral when SKU supports it and space is sufficient,
// fall back to managed when not. Mode-specific: VM checks StorageProfile/DiffDiskSettings,
// Machine API checks OSDiskType/OSDiskSizeGB.
func runSharedEphemeralDiskTests() {
	Context("Create - Ephemeral Disk", func() {
		It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"}, // Has large cache disk space
			})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if testOptions.IsAKSMachineAPIMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
			}
		})

		It("should fail to provision if ephemeral disk ask for is too large", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: v1.NodeSelectorOpGt,
				Values:   []string{"100000"},
			}) // No InstanceType will match this requirement
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
		})

		It("should select an ephemeral disk if LabelSKUStorageEphemeralOSMaxSize is set and os disk size fits", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: v1.NodeSelectorOpGt,
				Values:   []string{"0"},
			})
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](30)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			if testOptions.IsAKSMachineAPIMode() {
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			}
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if testOptions.IsAKSMachineAPIMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(30)))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(30)))
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
			}
		})

		It("should use ephemeral disk if supported, and set disk size to OSDiskSizeGB from node class", func() {
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](256)
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"},
			})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			if testOptions.IsAKSMachineAPIMode() {
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			}
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if testOptions.IsAKSMachineAPIMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(256)))
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(256)))
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
			}
		})

		It("should not use ephemeral disk if ephemeral is supported, but we don't have enough space", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2s_v3"},
			})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if testOptions.IsAKSMachineAPIMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeManaged))
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(128)))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
			}
		})
	})
}

// runSharedAdditionalTagsTests tests that additional tags configured in options
// are propagated to the created instance. Mode-specific: VM also checks NIC tags,
// Machine API checks the extra nodeclaim tag.
func runSharedAdditionalTagsTests() {
	Context("Create - Additional Tags", func() {
		It("should add additional tags to the created resource", func() {
			// Override ctx to add tags, preserving the current provision mode
			taggedOptions := test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(testOptions.ProvisionMode),
				UseSIG:        lo.ToPtr(testOptions.UseSIG),
				AdditionalTags: map[string]string{
					"karpenter.azure.com/test-tag": "test-value",
				},
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, taggedOptions)

			// Need a new environment with the tagged options for the instance provider to pick up tags
			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, taggedOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if testOptions.IsAKSMachineAPIMode() {
				// Machine API: tags on the AKS machine properties
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.azure.com_test-tag", lo.ToPtr("test-value")))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.azure.com_cluster", lo.ToPtr("test-cluster")))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("compute.aks.billing", lo.ToPtr("linux")))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.sh_nodepool", lo.ToPtr(nodePool.Name)))
				// Machine API has an extra tag linking to the NodeClaim
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			} else {
				// VM mode: tags on the VM and NIC
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Tags).To(Equal(map[string]*string{
					"karpenter.azure.com_test-tag": lo.ToPtr("test-value"),
					"karpenter.azure.com_cluster":  lo.ToPtr("test-cluster"),
					"compute.aks.billing":          lo.ToPtr("linux"),
					"karpenter.sh_nodepool":        lo.ToPtr(nodePool.Name),
				}))

				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic.Interface.Tags).To(Equal(map[string]*string{
					"karpenter.azure.com_test-tag": lo.ToPtr("test-value"),
					"karpenter.azure.com_cluster":  lo.ToPtr("test-cluster"),
					"compute.aks.billing":          lo.ToPtr("linux"),
					"karpenter.sh_nodepool":        lo.ToPtr(nodePool.Name),
				}))
			}
		})
	})
}
// Mode-specific assertions (which API was called, VM bootstrap/customData) are gated by testOptions.
func runSharedGPUTests() {
	Context("Create - GPU Workloads + Nodes", func() {
		It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			// Verify the selected VM size is NOT GPU-capable — mode-specific API check
			if testOptions.IsAKSMachineAPIMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				Expect(utils.IsNvidiaEnabledSKU(lo.FromPtr(aksMachine.Properties.Hardware.VMSize))).To(BeFalse())
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
				Expect(utils.IsNvidiaEnabledSKU(string(*vm.Properties.HardwareProfile.VMSize))).To(BeFalse())
			}

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

			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			// Cheapest GPU in the test set
			Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_NC16as_T4_v3"))

			// Verify GPU-capable VM size was selected — mode-specific API check
			if testOptions.IsAKSMachineAPIMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeTrue())
			} else {
				// VM-only: verify GPU-related settings in bootstrap customData
				// Note: ExpectDecodedCustomData pops from CalledWithInput, so we call it first
				customData := ExpectDecodedCustomData(azureEnv)
				Expect(customData).To(SatisfyAll(
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

				// Verify the VM was created with a GPU-capable size
				// (Note: CalledWithInput was already popped by ExpectDecodedCustomData above,
				//  but we re-provision or can check via node labels instead)
			}

			// Shared: Verify node has GPU resource and labels
			Expect(node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-name", "T4"))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-manufacturer", v1beta1.ManufacturerNvidia))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "1"))
		})
	})
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = AKSMachineAPIHeaderBatch", func() {
		BeforeEach(func() { setupAKSMachineAPIMode() })
		AfterEach(func() { teardownAKSMachineAPIMode() })
		// Mostly ported from VM test: "ImageReference" and "ImageProvider + Image Family"
		// Note: AKS Machine API does not support Community Image Gallery (CIG)
		Context("Create - ImageReference and ImageProvider + Image Family", func() {

			// Ported from VM test: "should use shared image gallery images when options are set to UseSIG"
			It("should use shared image gallery images", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Expect AKS machine to have a shared image gallery reference set via NodeImageVersion
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

				// NodeImageVersion should contain SIG identifier and subscription ID (converted from ImageReference.ID)
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring("AKSUbuntu"))
				Expect(nodeImageVersion).To(MatchRegexp(`^AKSUbuntu-.*-.*$`)) // Format: AKSUbuntu-<definition>-<version>

				// Clean up
				cluster.Reset()
				azureEnv.Reset()
			})

			// Note: Community Images tests are not ported since Community Images are not supported for AKS Machine API
			// This aligns with the warning in utils.GetAKSMachineNodeImageVersionFromImageID()

			// Ported from VM test DescribeTable: "should select the right Shared Image Gallery image for a given instance type"
			DescribeTable("should select the right Shared Image Gallery NodeImageVersion for a given instance type",
				func(instanceType string, imageFamily string, expectedImageDefinition string) {
					nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
					coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType}})

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
					aksMachine := createInput.AKSMachine
					Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

					// NodeImageVersion should contain the expected image definition
					nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
					Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
				},
				// Ported entries from VM test, covering SIG images for different generations and architectures
				Entry("Gen2, Gen1 instance type with AKSUbuntu image family", "Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition),
				Entry("Gen1 instance type with AKSUbuntu image family", "Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition),
				Entry("ARM instance type with AKSUbuntu image family", "Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition),
			)

			It("should select the right Shared Image Gallery NodeImageVersion for a given instance type, Gen2 instance type with AzureLinux image family", func() {
				instanceType := "Standard_D2_v5"
				imageFamily := v1beta1.AzureLinuxImageFamily
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{instanceType}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

				// NodeImageVersion should contain the expected image definition
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			})

			It("should select the right Shared Image Gallery NodeImageVersion for a given instance type, Gen1 instance type with AzureLinux image family", func() {
				instanceType := "Standard_D2_v3"
				imageFamily := v1beta1.AzureLinuxImageFamily
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{instanceType}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

				// NodeImageVersion should contain the expected image definition
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			})

			It("should select the right Shared Image Gallery NodeImageVersion for a given instance type, ARM instance type with AzureLinux image family", func() {
				instanceType := "Standard_D16plds_v5"
				imageFamily := v1beta1.AzureLinuxImageFamily
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{instanceType}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

				// NodeImageVersion should contain the expected image definition
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))

				// Clean up
				cluster.Reset()
				azureEnv.Reset()
			})
		})

		// Shared feature tests (run under both modes)
		runSharedGPUTests()
		runSharedAdditionalTagsTests()
		runSharedEphemeralDiskTests()

		Context("Create - Additional Configurations", func() {
			It("should handle configured NodeClass", func() {
				// Configure comprehensive NodeClass settings
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            lo.ToPtr("static"),
					CPUCFSQuota:                 lo.ToPtr(true),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
					FailSwapOn:                  lo.ToPtr(false),
				}
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)

				// Override context to use a BYO VNet instead of managed VNet
				// This allows testing custom subnet configuration (managed VNet doesn't allow custom subnets)
				byoClusterSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/cluster-subnet"
				byoOpts := test.Options(test.OptionsFields{
					ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPIHeaderBatch),
					UseSIG:        lo.ToPtr(true),
					SubnetID:      lo.ToPtr(byoClusterSubnetID),
				})
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
				ExpectProvisionedAndWaitForPromises(byoCtx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
				Expect(lo.FromPtr(aksMachine.Properties.Kubernetes.KubeletConfig.FailSwapOn)).To(BeFalse())

				// Verify image family configuration
				Expect(string(*aksMachine.Properties.OperatingSystem.OSSKU)).To(Equal(v1beta1.Ubuntu2204ImageFamily))

				// Verify subnet configuration (AKS machine should use the specified custom subnet)
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

				// Verify OS disk size configuration
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(100)))

				// Verify GPU node was selected (machine should be GPU-capable)
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
				_, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
				Expect(err).ToNot(HaveOccurred())

				// Verify machine was created with correct taints
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				machine := input.AKSMachine

				// Check that taints are configured
				// Currently, we will use "nodeInitializationTaints" field for all taints. More details in the relevant code (aksmachineinstancehelpers.go).
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Verify AKS machine was created with correct Karpenter-managed tags (not user overrides)
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := input.AKSMachine

				// Check that AKS machine has correct Karpenter-managed tags
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.sh_nodepool"))
				Expect(aksMachine.Properties.Tags["karpenter.sh_nodepool"]).To(Equal(&nodePool.Name))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
				Expect(aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(&testOptions.ClusterName))

				// Verify user-specified tags are ignored for Karpenter-managed keys
				Expect(*aksMachine.Properties.Tags["karpenter.sh_nodepool"]).ToNot(Equal("my-override-nodepool"))
				Expect(*aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).ToNot(Equal("my-override-cluster"))
			})
		})

		// Ported from VM test: "EncryptionAtHost"
		Context("Create - EncryptionAtHost", func() {
			It("should create AKS machine with EncryptionAtHost enabled when specified in AKSNodeClass", func() {
				if nodeClass.Spec.Security == nil {
					nodeClass.Spec.Security = &v1beta1.Security{}
				}
				nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(true)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				// Security profile should still exist but EncryptionAtHost should be false (default)
				Expect(aksMachine.Properties.Security).ToNot(BeNil())
				Expect(aksMachine.Properties.Security.EnableEncryptionAtHost).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Security.EnableEncryptionAtHost)).To(BeFalse())
			})
		})

		// Labels in the kubernetes.io/k8s.io domains were previously restricted by Karpenter core (<1.9.x)
		// and are now allowed on NodeClaims. However, kubelet cannot set most of them, so they should be
		// filtered out of AKS Machine NodeLabels (same as the VM path). Karpenter syncs them to the Node
		// directly, so they still appear on the Node object.
		DescribeTable("should handle previously reserved labels on AKS Machine create",
			func(label string, expectedInNodeLabels bool) {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements,
					karpv1.NodeSelectorRequirementWithMinValues{Key: label, Operator: v1.NodeSelectorOpIn, Values: []string{"custom-value"}},
				)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{label: "custom-value"}})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				// Label should always be on the Node (synced by Karpenter)
				Expect(node.Labels).To(HaveKeyWithValue(label, "custom-value"))

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())

				if expectedInNodeLabels {
					Expect(aksMachine.Properties.Kubernetes.NodeLabels).To(HaveKeyWithValue(label, lo.ToPtr("custom-value")))
				} else {
					Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(label))
				}
			},
			Entry("kubernetes.io (previously reserved)", "kubernetes.io/custom-label", false),
			Entry("k8s.io (previously reserved)", "k8s.io/custom-label", false),
			Entry("kubelet.kubernetes.io (kubelet-allowed)", "kubelet.kubernetes.io/custom-label", true),
		)

		Context("Create - LinuxOSConfig", func() {
			It("should create AKS machine with full LinuxOSConfig when specified in AKSNodeClass", func() {
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					FailSwapOn: lo.ToPtr(false),
				}
				nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
					SwapFileSize:               lo.ToPtr("1500Mi"),
					TransparentHugePageDefrag:  lo.ToPtr(v1beta1.TransparentHugePageDefragMadvise),
					TransparentHugePageEnabled: lo.ToPtr(v1beta1.TransparentHugePageEnabledAlways),
					Sysctls: &v1beta1.SysctlConfiguration{
						FsAioMaxNr:                     lo.ToPtr(int32(65536)),
						FsFileMax:                      lo.ToPtr(int32(12000)),
						FsInotifyMaxUserWatches:        lo.ToPtr(int32(781250)),
						FsNrOpen:                       lo.ToPtr(int32(8192)),
						KernelThreadsMax:               lo.ToPtr(int32(30000)),
						NetCoreNetdevMaxBacklog:        lo.ToPtr(int32(1000)),
						NetCoreOptmemMax:               lo.ToPtr(int32(20480)),
						NetCoreRmemDefault:             lo.ToPtr(int32(212992)),
						NetCoreRmemMax:                 lo.ToPtr(int32(212992)),
						NetCoreSomaxconn:               lo.ToPtr(int32(4096)),
						NetCoreWmemDefault:             lo.ToPtr(int32(212992)),
						NetCoreWmemMax:                 lo.ToPtr(int32(212992)),
						NetIPv4IPLocalPortRange:        lo.ToPtr("32768 60999"),
						NetIPv4NeighDefaultGcThresh1:   lo.ToPtr(int32(128)),
						NetIPv4NeighDefaultGcThresh2:   lo.ToPtr(int32(512)),
						NetIPv4NeighDefaultGcThresh3:   lo.ToPtr(int32(1024)),
						NetIPv4TCPFinTimeout:           lo.ToPtr(int32(60)),
						NetIPv4TCPKeepaliveProbes:      lo.ToPtr(int32(9)),
						NetIPv4TCPKeepaliveTime:        lo.ToPtr(int32(7200)),
						NetIPv4TCPMaxSynBacklog:        lo.ToPtr(int32(128)),
						NetIPv4TCPMaxTwBuckets:         lo.ToPtr(int32(8000)),
						NetIPv4TCPTwReuse:              lo.ToPtr(true),
						NetIPv4TCPKeepaliveIntvl:       lo.ToPtr(int32(75)),
						NetNetfilterNfConntrackBuckets: lo.ToPtr(int32(65536)),
						NetNetfilterNfConntrackMax:     lo.ToPtr(int32(131072)),
						VMMaxMapCount:                  lo.ToPtr(int32(65530)),
						VMSwappiness:                   lo.ToPtr(int32(60)),
						VMVfsCachePressure:             lo.ToPtr(int32(100)),
					},
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.LinuxProfile).ToNot(BeNil())
				linuxOSConfig := aksMachine.Properties.OperatingSystem.LinuxProfile.LinuxOSConfig
				Expect(linuxOSConfig).ToNot(BeNil())

				// Verify top-level fields
				Expect(lo.FromPtr(linuxOSConfig.SwapFileSizeMB)).To(Equal(int32(1500)))
				Expect(lo.FromPtr(linuxOSConfig.TransparentHugePageDefrag)).To(Equal("madvise"))
				Expect(lo.FromPtr(linuxOSConfig.TransparentHugePageEnabled)).To(Equal("always"))

				// Verify failSwapOn was wired through to kubelet config
				Expect(aksMachine.Properties.Kubernetes.KubeletConfig).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Kubernetes.KubeletConfig.FailSwapOn)).To(BeFalse())

				// Verify sysctl fields
				Expect(linuxOSConfig.Sysctls).ToNot(BeNil())
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.FsAioMaxNr)).To(Equal(int32(65536)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.FsFileMax)).To(Equal(int32(12000)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.FsInotifyMaxUserWatches)).To(Equal(int32(781250)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.FsNrOpen)).To(Equal(int32(8192)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.KernelThreadsMax)).To(Equal(int32(30000)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreNetdevMaxBacklog)).To(Equal(int32(1000)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreOptmemMax)).To(Equal(int32(20480)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreRmemDefault)).To(Equal(int32(212992)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreRmemMax)).To(Equal(int32(212992)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreSomaxconn)).To(Equal(int32(4096)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreWmemDefault)).To(Equal(int32(212992)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetCoreWmemMax)).To(Equal(int32(212992)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4IPLocalPortRange)).To(Equal("32768 60999"))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4NeighDefaultGcThresh1)).To(Equal(int32(128)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4NeighDefaultGcThresh2)).To(Equal(int32(512)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4NeighDefaultGcThresh3)).To(Equal(int32(1024)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TCPFinTimeout)).To(Equal(int32(60)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TCPKeepaliveProbes)).To(Equal(int32(9)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TCPKeepaliveTime)).To(Equal(int32(7200)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TCPMaxSynBacklog)).To(Equal(int32(128)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TCPMaxTwBuckets)).To(Equal(int32(8000)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TCPTwReuse)).To(BeTrue())
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetIPv4TcpkeepaliveIntvl)).To(Equal(int32(75)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetNetfilterNfConntrackBuckets)).To(Equal(int32(65536)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.NetNetfilterNfConntrackMax)).To(Equal(int32(131072)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.VMMaxMapCount)).To(Equal(int32(65530)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.VMSwappiness)).To(Equal(int32(60)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.VMVfsCachePressure)).To(Equal(int32(100)))
			})

			It("should create AKS machine with only sysctls when only sysctls are specified", func() {
				nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
					Sysctls: &v1beta1.SysctlConfiguration{
						VMMaxMapCount: lo.ToPtr(int32(262144)),
						VMSwappiness:  lo.ToPtr(int32(10)),
					},
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.OperatingSystem.LinuxProfile).ToNot(BeNil())
				linuxOSConfig := aksMachine.Properties.OperatingSystem.LinuxProfile.LinuxOSConfig
				Expect(linuxOSConfig).ToNot(BeNil())

				// Top-level fields should be nil
				Expect(linuxOSConfig.SwapFileSizeMB).To(BeNil())
				Expect(linuxOSConfig.TransparentHugePageDefrag).To(BeNil())
				Expect(linuxOSConfig.TransparentHugePageEnabled).To(BeNil())

				// Sysctls should be set
				Expect(linuxOSConfig.Sysctls).ToNot(BeNil())
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.VMMaxMapCount)).To(Equal(int32(262144)))
				Expect(lo.FromPtr(linuxOSConfig.Sysctls.VMSwappiness)).To(Equal(int32(10)))

				// Other sysctls should be nil
				Expect(linuxOSConfig.Sysctls.FsAioMaxNr).To(BeNil())
			})

			It("should create AKS machine with only TransparentHugePage settings when only TransparentHugePage is specified", func() {
				nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
					TransparentHugePageEnabled: lo.ToPtr(v1beta1.TransparentHugePageEnabledNever),
					TransparentHugePageDefrag:  lo.ToPtr(v1beta1.TransparentHugePageDefragDefer),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.OperatingSystem.LinuxProfile).ToNot(BeNil())
				linuxOSConfig := aksMachine.Properties.OperatingSystem.LinuxProfile.LinuxOSConfig
				Expect(linuxOSConfig).ToNot(BeNil())

				Expect(lo.FromPtr(linuxOSConfig.TransparentHugePageEnabled)).To(Equal("never"))
				Expect(lo.FromPtr(linuxOSConfig.TransparentHugePageDefrag)).To(Equal("defer"))
				Expect(linuxOSConfig.SwapFileSizeMB).To(BeNil())
				Expect(linuxOSConfig.Sysctls).To(BeNil())
			})

			It("should create AKS machine with only SwapFileSize when only swap is specified", func() {
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					FailSwapOn: lo.ToPtr(false),
				}
				nodeClass.Spec.LinuxOSConfig = &v1beta1.LinuxOSConfiguration{
					SwapFileSize: lo.ToPtr("2Gi"),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.OperatingSystem.LinuxProfile).ToNot(BeNil())
				linuxOSConfig := aksMachine.Properties.OperatingSystem.LinuxProfile.LinuxOSConfig
				Expect(linuxOSConfig).ToNot(BeNil())
				Expect(lo.FromPtr(linuxOSConfig.SwapFileSizeMB)).To(Equal(int32(2048)))
				Expect(linuxOSConfig.TransparentHugePageDefrag).To(BeNil())
				Expect(linuxOSConfig.TransparentHugePageEnabled).To(BeNil())
				Expect(linuxOSConfig.Sysctls).To(BeNil())
			})

			It("should create AKS machine without LinuxProfile when LinuxOSConfig is not specified", func() {
				// Explicitly ensure LinuxOSConfig is not set
				nodeClass.Spec.LinuxOSConfig = nil
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.LinuxProfile).To(BeNil())
			})
		})

		Context("Create - ArtifactStreaming", func() {
			It("should set ArtifactStreamingProfile when explicitly enabled", func() {
				nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreaming{
					Enabled: lo.ToPtr(true),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
				Expect(aksMachine.Properties.Kubernetes.ArtifactStreamingProfile).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Kubernetes.ArtifactStreamingProfile.Enabled)).To(BeTrue())
			})

			It("should not set ArtifactStreamingProfile when not specified (defaults to disabled)", func() {
				nodeClass.Spec.ArtifactStreaming = nil
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
				Expect(aksMachine.Properties.Kubernetes.ArtifactStreamingProfile).To(BeNil())
			})

			It("should not set ArtifactStreamingProfile when explicitly disabled", func() {
				nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreaming{
					Enabled: lo.ToPtr(false),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
				Expect(aksMachine.Properties.Kubernetes.ArtifactStreamingProfile).To(BeNil())
			})

			It("should not set ArtifactStreamingProfile for ARM64 instance types even when enabled", func() {
				nodeClass.Spec.ArtifactStreaming = &v1beta1.ArtifactStreaming{
					Enabled: lo.ToPtr(true),
				}
				// ARM64 does not support artifact streaming; IsArtifactStreamingEnabled returns false for arm64.
				// Verify through the NodeClass API directly since the test environment may not have ARM64 instance types.
				Expect(nodeClass.IsArtifactStreamingEnabled("arm64")).To(BeFalse())
				Expect(nodeClass.IsArtifactStreamingEnabled("amd64")).To(BeTrue())
			})
		})

		Context("Create - LocalDNS", func() {
			It("should set LocalDNSProfile with mode Required", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModeRequired,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.LocalDNSProfile.Mode)).To(Equal(armcontainerservice.LocalDNSModeRequired))
				Expect(aksMachine.Properties.LocalDNSProfile.VnetDNSOverrides).To(HaveLen(2))
				Expect(aksMachine.Properties.LocalDNSProfile.KubeDNSOverrides).To(HaveLen(2))
			})

			It("should not set LocalDNSProfile when LocalDNS is nil", func() {
				nodeClass.Spec.LocalDNS = nil
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).To(BeNil())
			})

			It("should correctly convert override fields including durations", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode: v1beta1.LocalDNSModeRequired,
					VnetDNSOverrides: []v1beta1.LocalDNSZoneOverride{
						{
							Zone:               ".",
							ForwardDestination: v1beta1.LocalDNSForwardDestinationVnetDNS,
							QueryLogging:       v1beta1.LocalDNSQueryLoggingLog,
							Protocol:           v1beta1.LocalDNSProtocolForceTCP,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicyRoundRobin,
							MaxConcurrent:      lo.ToPtr(int32(50)),
							CacheDuration:      karpv1.MustParseNillableDuration("30s"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("60s"),
							ServeStale:         v1beta1.LocalDNSServeStaleImmediate,
						},
						{
							Zone:               "cluster.local",
							ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
							QueryLogging:       v1beta1.LocalDNSQueryLoggingLog,
							Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
							MaxConcurrent:      lo.ToPtr(int32(10)),
							CacheDuration:      karpv1.MustParseNillableDuration("10s"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("5s"),
							ServeStale:         v1beta1.LocalDNSServeStaleVerify,
						},
					},
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).ToNot(BeNil())

				vnetOverride := aksMachine.Properties.LocalDNSProfile.VnetDNSOverrides["."]
				Expect(vnetOverride).ToNot(BeNil())
				Expect(lo.FromPtr(vnetOverride.ForwardDestination)).To(Equal(armcontainerservice.LocalDNSForwardDestinationVnetDNS))
				Expect(lo.FromPtr(vnetOverride.QueryLogging)).To(Equal(armcontainerservice.LocalDNSQueryLoggingLog))
				Expect(lo.FromPtr(vnetOverride.Protocol)).To(Equal(armcontainerservice.LocalDNSProtocolForceTCP))
				Expect(lo.FromPtr(vnetOverride.ForwardPolicy)).To(Equal(armcontainerservice.LocalDNSForwardPolicyRoundRobin))
				Expect(lo.FromPtr(vnetOverride.MaxConcurrent)).To(Equal(int32(50)))
				Expect(lo.FromPtr(vnetOverride.CacheDurationInSeconds)).To(Equal(int32(30)))
				Expect(lo.FromPtr(vnetOverride.ServeStaleDurationInSeconds)).To(Equal(int32(60)))
				Expect(lo.FromPtr(vnetOverride.ServeStale)).To(Equal(armcontainerservice.LocalDNSServeStaleImmediate))
			})

			It("should set LocalDNSProfile with mode Disabled", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModeDisabled,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.LocalDNSProfile.Mode)).To(Equal(armcontainerservice.LocalDNSModeDisabled))
			})

			It("should set LocalDNSProfile with mode Preferred", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModePreferred,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.LocalDNSProfile.Mode)).To(Equal(armcontainerservice.LocalDNSModePreferred))
			})

			It("should correctly convert KubeDNSOverrides field values", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModeRequired,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: []v1beta1.LocalDNSZoneOverride{
						{
							Zone:               ".",
							ForwardDestination: v1beta1.LocalDNSForwardDestinationClusterCoreDNS,
							QueryLogging:       v1beta1.LocalDNSQueryLoggingLog,
							Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
							ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
							MaxConcurrent:      lo.ToPtr(int32(25)),
							CacheDuration:      karpv1.MustParseNillableDuration("15s"),
							ServeStaleDuration: karpv1.MustParseNillableDuration("45s"),
							ServeStale:         v1beta1.LocalDNSServeStaleVerify,
						},
						validLocalDNSZoneOverride("cluster.local", v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
					},
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).ToNot(BeNil())
				Expect(aksMachine.Properties.LocalDNSProfile.KubeDNSOverrides).To(HaveLen(2))

				kubeOverride := aksMachine.Properties.LocalDNSProfile.KubeDNSOverrides["."]
				Expect(kubeOverride).ToNot(BeNil())
				Expect(lo.FromPtr(kubeOverride.ForwardDestination)).To(Equal(armcontainerservice.LocalDNSForwardDestinationClusterCoreDNS))
				Expect(lo.FromPtr(kubeOverride.QueryLogging)).To(Equal(armcontainerservice.LocalDNSQueryLoggingLog))
				Expect(lo.FromPtr(kubeOverride.Protocol)).To(Equal(armcontainerservice.LocalDNSProtocolPreferUDP))
				Expect(lo.FromPtr(kubeOverride.ForwardPolicy)).To(Equal(armcontainerservice.LocalDNSForwardPolicySequential))
				Expect(lo.FromPtr(kubeOverride.MaxConcurrent)).To(Equal(int32(25)))
				Expect(lo.FromPtr(kubeOverride.CacheDurationInSeconds)).To(Equal(int32(15)))
				Expect(lo.FromPtr(kubeOverride.ServeStaleDurationInSeconds)).To(Equal(int32(45)))
				Expect(lo.FromPtr(kubeOverride.ServeStale)).To(Equal(armcontainerservice.LocalDNSServeStaleVerify))
			})
		})
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() { setupAKSScriptlessMode() })
		AfterEach(func() { teardownAKSScriptlessMode() })

		// Shared feature tests (run under both modes)
		runSharedGPUTests()
		runSharedAdditionalTagsTests()
		runSharedEphemeralDiskTests()

		// AKSScriptless-specific feature tests (VM provisioning e2e)
		Context("AKSScriptless-specific", func() {
			Context("Subnet", func() {
				It("should use the VNET_SUBNET_ID", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
					Expect(nic).NotTo(BeNil())
					Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet"))
				})
				It("should produce all required azure cni labels", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					decodedString := ExpectDecodedCustomData(azureEnv)
					Expect(decodedString).To(SatisfyAll(
						ContainSubstring("kubernetes.azure.com/ebpf-dataplane=cilium"),
						ContainSubstring("kubernetes.azure.com/network-subnet=aks-subnet"),
						ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c"),
						ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
						ContainSubstring("kubernetes.azure.com/azure-cni-overlay=true"),
					))
				})
				It("should include stateless CNI label for kubernetes 1.34+ set to true", func() {
					nodeClass.Status.KubernetesVersion = lo.ToPtr("1.34.0")
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					decodedString := ExpectDecodedCustomData(azureEnv)
					Expect(decodedString).To(SatisfyAll(
						ContainSubstring("kubernetes.azure.com/network-stateless-cni=true"),
					))
				})
				It("should include stateless CNI label for kubernetes < 1.34 set to false", func() {
					nodeClass.Status.KubernetesVersion = lo.ToPtr("1.33.0")
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					decodedString := ExpectDecodedCustomData(azureEnv)
					Expect(decodedString).To(SatisfyAll(
						ContainSubstring("kubernetes.azure.com/network-stateless-cni=false"),
					))
				})
				It("should use the subnet specified in the nodeclass", func() {
					nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpenter/subnets/nodeclassSubnet")
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
					Expect(nic).NotTo(BeNil())
					Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpenter/subnets/nodeclassSubnet"))
				})
			})
		})
	})
})

// validLocalDNSOverridePair returns a minimal valid pair of LocalDNSZoneOverrides
// for "." and "cluster.local" zones (both required by CRD validation).
func validLocalDNSOverridePair(rootForwardDest v1beta1.LocalDNSForwardDestination) []v1beta1.LocalDNSZoneOverride {
	return []v1beta1.LocalDNSZoneOverride{
		validLocalDNSZoneOverride(".", rootForwardDest),
		validLocalDNSZoneOverride("cluster.local", v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
	}
}

func validLocalDNSZoneOverride(zone string, forwardDest v1beta1.LocalDNSForwardDestination) v1beta1.LocalDNSZoneOverride {
	return v1beta1.LocalDNSZoneOverride{
		Zone:               zone,
		ForwardDestination: forwardDest,
		QueryLogging:       v1beta1.LocalDNSQueryLoggingLog,
		Protocol:           v1beta1.LocalDNSProtocolPreferUDP,
		ForwardPolicy:      v1beta1.LocalDNSForwardPolicySequential,
		MaxConcurrent:      lo.ToPtr(int32(10)),
		CacheDuration:      karpv1.MustParseNillableDuration("30s"),
		ServeStaleDuration: karpv1.MustParseNillableDuration("30s"),
		ServeStale:         v1beta1.LocalDNSServeStaleDisable,
	}
}
