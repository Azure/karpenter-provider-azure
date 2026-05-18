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
	"strconv"
	"strings"
	"time"

	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

// runSharedManagedTagProtectionTests verifies that Karpenter-managed tags cannot be overridden
// by user-specified tags in nodeClass.Spec.Tags. The Tags() function in launchtemplate/tags.go
// applies defaultTags AFTER nodeClassTags via lo.Assign, ensuring managed keys always win.
// This behavior is identical across provision modes.
func runSharedManagedTagProtectionTests() {
	Context("Create - Managed Tag Protection", func() {
		It("should not allow the user to override Karpenter-managed tags", func() {
			nodeClass.Spec.Tags = map[string]string{
				"karpenter.azure.com/cluster": "my-override-cluster",
				"karpenter.sh/nodepool":       "my-override-nodepool",
			}
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
				// Karpenter-managed tags should have system values, not user overrides
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.sh_nodepool", lo.ToPtr(nodePool.Name)))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.azure.com_cluster", lo.ToPtr(testOptions.ClusterName)))
				Expect(*aksMachine.Properties.Tags["karpenter.sh_nodepool"]).ToNot(Equal("my-override-nodepool"))
				Expect(*aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).ToNot(Equal("my-override-cluster"))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				// Karpenter-managed tags should have system values, not user overrides
				Expect(vm.Tags).To(HaveKeyWithValue("karpenter.sh_nodepool", lo.ToPtr(nodePool.Name)))
				Expect(vm.Tags).To(HaveKeyWithValue("karpenter.azure.com_cluster", lo.ToPtr(testOptions.ClusterName)))
				Expect(*vm.Tags["karpenter.sh_nodepool"]).ToNot(Equal("my-override-nodepool"))
				Expect(*vm.Tags["karpenter.azure.com_cluster"]).ToNot(Equal("my-override-cluster"))

				// VM also has NIC — check NIC tags are protected too
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic.Interface.Tags).To(HaveKeyWithValue("karpenter.sh_nodepool", lo.ToPtr(nodePool.Name)))
				Expect(nic.Interface.Tags).To(HaveKeyWithValue("karpenter.azure.com_cluster", lo.ToPtr(testOptions.ClusterName)))
			}
		})
	})
}

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
		// Mode-specific: Machine API uses NodeImageVersion; VM uses StorageProfile.ImageReference.ID/CommunityGalleryImageID.
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
				azureEnv.Reset(ctx)
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
				azureEnv.Reset(ctx)
			})
		})

		// Shared feature tests (run under both modes)
		runSharedGPUTests()
		runSharedAdditionalTagsTests()
		runSharedManagedTagProtectionTests()
		runSharedEphemeralDiskTests()

		// Mode-specific: tests Machine API struct fields (KubeletConfig, Subnet, Tags, OSDiskSize) in combination.
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
		})

		// Ported from VM test: "EncryptionAtHost"
		// Mode-specific: EncryptionAtHost is a Machine API security field. VM handles this differently (not via Karpenter).
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

		// Mode-specific: LinuxOSConfig (LinuxProfile) is a Machine API-only feature. VM mode has no equivalent.
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

		// Mode-specific: ArtifactStreamingProfile is a Machine API-only feature. VM mode has no equivalent.
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

		// Mode-specific: LocalDNSProfile is a Machine API struct field. VM handles LocalDNS via instancetype filtering.
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

			It("should rewrite Preferred to Required on the wire when Status.LocalDNSState=Enabled", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModePreferred,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
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
			})

			It("should rewrite Preferred to Disabled on the wire when Status.LocalDNSState is unset", func() {
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModePreferred,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
				stored := nodeClass.DeepCopy()
				nodeClass.Status.LocalDNSState = nil
				Expect(env.Client.Status().Patch(ctx, nodeClass, client.MergeFrom(stored))).To(Succeed())

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine

				Expect(aksMachine.Properties.LocalDNSProfile).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.LocalDNSProfile.Mode)).To(Equal(armcontainerservice.LocalDNSModeDisabled))
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
		runSharedManagedTagProtectionTests()
		runSharedEphemeralDiskTests()

		// AKSScriptless-specific feature tests (VM provisioning e2e)
		Context("AKSScriptless-specific", func() {
			// Mode-specific: VM NIC subnet management. Machine API doesn't manage NICs.
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

			// Mode-specific: customData --cluster-dns kubelet flag. Machine API doesn't use customData.
			Context("Custom DNS", func() {
				It("should support provisioning with custom DNS server from options", func() {
					ctx = options.ToContext(
						ctx,
						test.Options(test.OptionsFields{
							ClusterDNSServiceIP: lo.ToPtr("10.244.0.1"),
						}),
					)

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					customData := ExpectDecodedCustomData(azureEnv)
					expectedFlags := map[string]string{
						"cluster-dns": "10.244.0.1",
					}
					ExpectKubeletFlags(azureEnv, customData, expectedFlags)
				})
			})

			// ImageReference + ImageProvider + Image Family (moved from instancetype/suite_test.go)
			// Mode-specific: CIG is VM-only. SIG checks StorageProfile.ImageReference.ID (ARM resource ID format).
			Context("ImageReference", func() {
				It("should use shared image gallery images when options are set to UseSIG", func() {
					options := test.Options(test.OptionsFields{
						UseSIG: lo.ToPtr(true),
					})
					ctx = options.ToContext(ctx)
					statusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, options.ParsedDiskEncryptionSetID, options.NetworkPolicy, options.NetworkPlugin)

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					// Expect virtual machine to have a shared image gallery id set on it
					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
					Expect(vm.Properties.StorageProfile.ImageReference.ID).ShouldNot(BeNil())
					Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).Should(BeNil())

					Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(options.SIGSubscriptionID))
					Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring("AKSUbuntu"))
				})
				It("should use Community Images when options are set to UseSIG=false", func() {
					options := test.Options(test.OptionsFields{
						UseSIG: lo.ToPtr(false),
					})
					ctx = options.ToContext(ctx)
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).Should(Not(BeNil()))

				})

			})

			// Mode-specific: checks VM StorageProfile.ImageReference format (SIG resource IDs, CIG gallery URLs).
			Context("ImageProvider + Image Family", func() {
				// Note: the test k8s version is determined at runtime by the fake server.
				// We hardcode "1.29.5" here because DescribeTable Entry params are evaluated during
				// tree construction (before BeforeSuite), so env.KubernetesInterface is not yet available.
				// This matches the fake server's version in test environments.
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3("1.29.5")
				azureLinuxGen2ImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)
				azureLinuxGen1ImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)
				azureLinuxGen2ArmImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)

				DescribeTable("should select the right Shared Image Gallery image for a given instance type", func(instanceType string, imageFamily string, expectedImageDefinition string, expectedGalleryRG string, expectedGalleryURL string) {
					options := test.Options(test.OptionsFields{
						UseSIG: lo.ToPtr(true),
					})
					ctx = options.ToContext(ctx)
					statusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, options.ParsedDiskEncryptionSetID, options.NetworkPolicy, options.NetworkPlugin)

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

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())

					expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s", options.SIGSubscriptionID, expectedGalleryRG, expectedGalleryURL, expectedImageDefinition)
					Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(expectedPrefix))

				},

					Entry("Gen2, Gen1 instance type with AKSUbuntu image family", "Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
					Entry("Gen1 instance type with AKSUbuntu image family", "Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
					Entry("ARM instance type with AKSUbuntu image family", "Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
					Entry("Gen2 instance type with AzureLinux image family", "Standard_D2_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName),
					Entry("Gen1 instance type with AzureLinux image family", "Standard_D2_v3", v1beta1.AzureLinuxImageFamily, azureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName),
					Entry("ARM instance type with AzureLinux image family", "Standard_D16plds_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName),
				)
				DescribeTable("should select the right image for a given instance type",
					func(instanceType string, imageFamily string, expectedImageDefinition string, expectedGalleryURL string) {
						statusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
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

						Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
						Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).ToNot(BeNil())
						parts := strings.Split(*vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID, "/")
						Expect(parts[2]).To(Equal(expectedGalleryURL))
						Expect(parts[4]).To(Equal(expectedImageDefinition))

						// Need to reset env since we are doing these nested tests
						cluster.Reset()
						azureEnv.Reset(ctx)
					},
					Entry("Gen2, Gen1 instance type with AKSUbuntu image family",
						"Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
					Entry("Gen1 instance type with AKSUbuntu image family",
						"Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
					Entry("ARM instance type with AKSUbuntu image family",
						"Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
					Entry("Gen2 instance type with AzureLinux image family",
						"Standard_D2_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
					Entry("Gen1 instance type with AzureLinux image family",
						"Standard_D2_v3", v1beta1.AzureLinuxImageFamily, azureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
					Entry("ARM instance type with AzureLinux image family",
						"Standard_D16plds_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				)
			})

			// Bootstrap (moved from instancetype/suite_test.go)
			// Mode-specific: CSE/customData bootstrap (kubelet flags, credential provider, taints, CNI labels). Machine API doesn't use CSE.
			Context("Bootstrap", func() {
				var (
					kubeletFlags          string
					customData            string
					minorVersion          uint64
					credentialProviderURL string
				)
				BeforeEach(func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					customData = ExpectDecodedCustomData(azureEnv)
					kubeletFlags = ExpectKubeletFlagsPassed(customData)

					k8sVersion, err := azureEnv.KubernetesVersionProvider.KubeServerVersion(ctx)
					Expect(err).To(BeNil())
					minorVersion = semver.MustParse(k8sVersion).Minor
					credentialProviderURL = bootstrap.CredentialProviderURL(k8sVersion, "amd64")
				})

				It("should include or exclude --keep-terminated-pod-volumes based on kubelet version", func() {
					if minorVersion < 31 {
						Expect(kubeletFlags).To(ContainSubstring("--keep-terminated-pod-volumes"))
					} else {
						Expect(kubeletFlags).ToNot(ContainSubstring("--keep-terminated-pod-volumes"))
					}
				})

				It("should include correct flags and credential provider URL when CredentialProviderURL is not empty", func() {
					if credentialProviderURL != "" {
						Expect(kubeletFlags).ToNot(ContainSubstring("--azure-container-registry-config"))
						Expect(kubeletFlags).To(ContainSubstring("--image-credential-provider-config=/var/lib/kubelet/credential-provider-config.yaml"))
						Expect(kubeletFlags).To(ContainSubstring("--image-credential-provider-bin-dir=/var/lib/kubelet/credential-provider"))
						Expect(customData).To(ContainSubstring(credentialProviderURL))
					}
				})

				It("should include correct flags when CredentialProviderURL is empty", func() {
					if credentialProviderURL == "" {
						Expect(kubeletFlags).To(ContainSubstring("--azure-container-registry-config"))
						Expect(kubeletFlags).ToNot(ContainSubstring("--image-credential-provider-config"))
						Expect(kubeletFlags).ToNot(ContainSubstring("--image-credential-provider-bin-dir"))
					}
				})

				It("should include karpenter.sh/unregistered taint", func() {
					Expect(kubeletFlags).To(ContainSubstring("--register-with-taints=" + karpv1.UnregisteredNoExecuteTaint.ToString()))
				})
			})

			// Mode-specific: customData network plugin and node label configuration. Machine API doesn't use customData.
			DescribeTable("Azure CNI node labels and agentbaker network plugin", func(
				networkPlugin, networkPluginMode, networkDataplane, expectedAgentBakerNetPlugin string,
				expectedNodeLabels sets.Set[string]) {
				options := test.Options(test.OptionsFields{
					NetworkPlugin:     lo.ToPtr(networkPlugin),
					NetworkPluginMode: lo.ToPtr(networkPluginMode),
					NetworkDataplane:  lo.ToPtr(networkDataplane),
				})
				ctx = options.ToContext(ctx)

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				customData := ExpectDecodedCustomData(azureEnv)

				Expect(customData).To(ContainSubstring(fmt.Sprintf("NETWORK_PLUGIN=%s", expectedAgentBakerNetPlugin)))

				for label := range expectedNodeLabels {
					Expect(customData).To(ContainSubstring(label))
				}
			},
				Entry("Azure CNI V1",
					"azure", "", "",
					"azure", sets.New[string]()),
				Entry("Azure CNI w Overlay",
					"azure", "overlay", "",
					"none",
					sets.New(
						"kubernetes.azure.com/azure-cni-overlay=true",
						"kubernetes.azure.com/network-subnet=aks-subnet",
						"kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c",
						"kubernetes.azure.com/podnetwork-type=overlay",
					)),
				Entry("Network Plugin none",
					"none", "", "", "none",
					sets.New[string]()),
				Entry("Azure CNI w Overlay w Cilium",
					"azure", "overlay", "cilium",
					"none",
					sets.New(
						"kubernetes.azure.com/azure-cni-overlay=true",
						"kubernetes.azure.com/network-subnet=aks-subnet",
						"kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c",
						"kubernetes.azure.com/podnetwork-type=overlay",
						"kubernetes.azure.com/ebpf-dataplane=cilium",
					)),
				Entry("Cilium w feature flag Microsoft.ContainerService/EnableCiliumNodeSubnet",
					"azure", "", "cilium",
					"none",
					sets.New("kubernetes.azure.com/ebpf-dataplane=cilium")),
			)

			// Basic (moved from instancetype/suite_test.go)
			// Contains both unit tests (requirements, capacity, label coverage, skewer propagation)
			// and VM provisioning e2e tests (label scheduling, kubelet label writing).
			// Kept together because they share the WellKnownLabelEntry infrastructure.
			Context("Basic", func() {
				var instanceTypes corecloudprovider.InstanceTypes
				var err error
				BeforeEach(func() {
					// disable VM memory overhead for simpler capacity testing
					ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
						VMMemoryOverheadPercent: lo.ToPtr[float64](0),
					}))
					instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					// Also set up bootstrap env for bootstrap-specific label tests
					setupBootstrappingClientMode()
				})
				AfterEach(func() {
					teardownBootstrappingClientMode()
				})

				It("should have all the requirements on every sku", func() {
					for _, instanceType := range instanceTypes {
						reqs := instanceType.Requirements

						Expect(reqs.Has(v1.LabelArchStable)).To(BeTrue())
						Expect(reqs.Has(v1.LabelOSStable)).To(BeTrue())
						Expect(reqs.Has(v1.LabelInstanceTypeStable)).To(BeTrue())

						Expect(reqs.Has(v1beta1.LabelSKUName)).To(BeTrue())

						Expect(reqs.Has(v1beta1.LabelSKUStoragePremiumCapable)).To(BeTrue())
						Expect(reqs.Has(v1beta1.LabelSKUAcceleratedNetworking)).To(BeTrue())
						Expect(reqs.Has(v1beta1.LabelSKUHyperVGeneration)).To(BeTrue())
						Expect(reqs.Has(v1beta1.LabelSKUStorageEphemeralOSMaxSize)).To(BeTrue())
					}
				})
				It("boolean requirements should have a value, either 'true' or 'false'", func() {
					for _, instanceType := range instanceTypes {
						reqs := instanceType.Requirements
						Expect(reqs.Get(v1beta1.LabelSKUStoragePremiumCapable).Values()).To(HaveLen(1))
						Expect(reqs.Get(v1beta1.LabelSKUStoragePremiumCapable).Values()[0]).To(SatisfyAny(Equal("true"), Equal("false")))
						Expect(reqs.Get(v1beta1.LabelSKUAcceleratedNetworking).Values()).To(HaveLen(1))
						Expect(reqs.Get(v1beta1.LabelSKUAcceleratedNetworking).Values()[0]).To(SatisfyAny(Equal("true"), Equal("false")))
					}
				})

				It("should have all compute capacity", func() {
					for _, instanceType := range instanceTypes {
						capList := instanceType.Capacity
						Expect(capList).To(HaveKey(v1.ResourceCPU))
						Expect(capList).To(HaveKey(v1.ResourceMemory))
						Expect(capList).To(HaveKey(v1.ResourcePods))
						Expect(capList).To(HaveKey(v1.ResourceEphemeralStorage))
					}
				})

				// TODO: Is this stuff really about Provider List? Feels like no, should we put it elsewhere?
				type WellKnownLabelEntry struct {
					Name      string
					Label     string
					ValueFunc func() string
					SetupFunc func()
					// ExpectedInKubeletLabels indicates if we expect to see this in the KUBELET_NODE_LABELS section of the custom script extension.
					// If this is false it means that Karpenter will not set it on the node via KUBELET_NODE_LABELS.
					// It does NOT mean that it will not be on the resulting Node object in a real cluster, as it may be written by another process.
					// We expect that if ExpectedOnNode is set, ExpectedInKubeletLabels is also set.
					ExpectedInKubeletLabels bool
					// ExpectedOnNode indicates if we expect to see this on the node.
					// If this is false it means is that Karpenter will not set it on the node directly via kube-apiserver.
					// It does NOT mean that it will not be on the resulting Node object in a real cluster, as it may be written as part of KUBELET_NODE_LABELS (see above)
					// or by another process. We're asserting on this distinction currently because it helps clarify who is doing what
					ExpectedOnNode bool
				}

				// requireFunc returns a SetupFunc that adds a label requirement to the NodePool
				requireFunc := func(key, value string) func() {
					return func() {
						nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements,
							karpv1.NodeSelectorRequirementWithMinValues{Key: key, Operator: v1.NodeSelectorOpIn, Values: []string{value}},
						)
					}
				}

				// TODO: Is this stuff really about Provider List? Feels like no, should we put it elsewhere?
				entries := []WellKnownLabelEntry{
					// Well known
					{Name: v1.LabelTopologyRegion, Label: v1.LabelTopologyRegion, ValueFunc: func() string { return fake.Region }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: karpv1.NodePoolLabelKey, Label: karpv1.NodePoolLabelKey, ValueFunc: func() string { return nodePool.Name }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1.LabelTopologyZone, Label: v1.LabelTopologyZone, ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1.LabelInstanceTypeStable, Label: v1.LabelInstanceTypeStable, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1.LabelOSStable, Label: v1.LabelOSStable, ValueFunc: func() string { return "linux" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1.LabelArchStable, Label: v1.LabelArchStable, ValueFunc: func() string { return "amd64" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: karpv1.CapacityTypeLabelKey, Label: karpv1.CapacityTypeLabelKey, ValueFunc: func() string { return "on-demand" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelPlacementScope, Label: v1beta1.LabelPlacementScope, ValueFunc: func() string { return v1beta1.PlacementScopeZonal }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					// Well Known to AKS
					{Name: v1beta1.LabelSKUName, Label: v1beta1.LabelSKUName, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUFamily, Label: v1beta1.LabelSKUFamily, ValueFunc: func() string { return "N" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUSeries, Label: v1beta1.LabelSKUSeries, ValueFunc: func() string { return "NCads_v4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUVersion, Label: v1beta1.LabelSKUVersion, ValueFunc: func() string { return "4" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUStorageEphemeralOSMaxSize, Label: v1beta1.LabelSKUStorageEphemeralOSMaxSize, ValueFunc: func() string { return "429" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUAcceleratedNetworking, Label: v1beta1.LabelSKUAcceleratedNetworking, ValueFunc: func() string { return "true" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUStoragePremiumCapable, Label: v1beta1.LabelSKUStoragePremiumCapable, ValueFunc: func() string { return "true" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUGPUName, Label: v1beta1.LabelSKUGPUName, ValueFunc: func() string { return "A100" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUGPUManufacturer, Label: v1beta1.LabelSKUGPUManufacturer, ValueFunc: func() string { return "nvidia" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUGPUCount, Label: v1beta1.LabelSKUGPUCount, ValueFunc: func() string { return "1" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUCPU, Label: v1beta1.LabelSKUCPU, ValueFunc: func() string { return "24" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.LabelSKUMemory, Label: v1beta1.LabelSKUMemory, ValueFunc: func() string { return "8192" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					// AKS domain
					{Name: v1beta1.AKSLabelCPU, Label: v1beta1.AKSLabelCPU, ValueFunc: func() string { return "24" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelMemory, Label: v1beta1.AKSLabelMemory, ValueFunc: func() string { return "8192" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelMode + "=user", Label: v1beta1.AKSLabelMode, ValueFunc: func() string { return "user" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelMode + "=system", Label: v1beta1.AKSLabelMode, ValueFunc: func() string { return "system" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelScaleSetPriority + "=regular", Label: v1beta1.AKSLabelScaleSetPriority, ValueFunc: func() string { return "regular" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelScaleSetPriority + "=spot", Label: v1beta1.AKSLabelScaleSetPriority, ValueFunc: func() string { return "spot" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelPriority + "=regular", Label: v1beta1.AKSLabelPriority, ValueFunc: func() string { return "regular" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelPriority + "=spot", Label: v1beta1.AKSLabelPriority, ValueFunc: func() string { return "spot" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{Name: v1beta1.AKSLabelOSSKU, Label: v1beta1.AKSLabelOSSKU, ValueFunc: func() string { return "Ubuntu" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					{
						Name:  v1beta1.AKSLabelFIPSEnabled,
						Label: v1beta1.AKSLabelFIPSEnabled,
						// Needs special setup because it only works on FIPS
						SetupFunc: func() {
							testOptions.UseSIG = true
							ctx = options.ToContext(ctx, testOptions)

							nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
							nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
							test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
						},
						ValueFunc:               func() string { return "true" },
						ExpectedInKubeletLabels: true,
						ExpectedOnNode:          true,
					},
					// Deprecated Labels -- note that these are not expected in kubelet labels or on the node.
					// They are written by CloudProvider so don't need to be sent to kubelet, and they aren't required on the node object because Karpenter does a mapping from
					// the new labels to the old labels for compatibility.
					{Name: v1.LabelFailureDomainBetaRegion, Label: v1.LabelFailureDomainBetaRegion, ValueFunc: func() string { return fake.Region }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
					{Name: v1.LabelFailureDomainBetaZone, Label: v1.LabelFailureDomainBetaZone, ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
					{Name: "beta.kubernetes.io/arch", Label: "beta.kubernetes.io/arch", ValueFunc: func() string { return "amd64" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
					{Name: "beta.kubernetes.io/os", Label: "beta.kubernetes.io/os", ValueFunc: func() string { return "linux" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
					{Name: v1.LabelInstanceType, Label: v1.LabelInstanceType, ValueFunc: func() string { return "Standard_NC24ads_A100_v4" }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
					{Name: "topology.disk.csi.azure.com/zone", Label: "topology.disk.csi.azure.com/zone", ValueFunc: func() string { return fakeZone1 }, ExpectedInKubeletLabels: false, ExpectedOnNode: false},
					// Unsupported labels
					{Name: v1.LabelWindowsBuild, Label: v1.LabelWindowsBuild, ValueFunc: func() string { return "window" }, ExpectedInKubeletLabels: true, ExpectedOnNode: false},
					// Cluster Label
					{Name: v1beta1.AKSLabelCluster, Label: v1beta1.AKSLabelCluster, ValueFunc: func() string { return "test-resourceGroup" }, ExpectedInKubeletLabels: true, ExpectedOnNode: true},
					// Previously reserved labels (kubernetes.io/k8s.io domains) that were restricted by Karpenter core before 1.9.x.
					// These are now allowed on NodeClaims and synced to the Node by Karpenter, but kubelet cannot set them.
					{
						Name:                    "kubernetes.io (previously reserved)",
						Label:                   "kubernetes.io/custom-label",
						SetupFunc:               requireFunc("kubernetes.io/custom-label", "custom-value"),
						ValueFunc:               func() string { return "custom-value" },
						ExpectedInKubeletLabels: false,
						ExpectedOnNode:          true,
					},
					{
						Name:                    "k8s.io (previously reserved)",
						Label:                   "k8s.io/custom-label",
						SetupFunc:               requireFunc("k8s.io/custom-label", "custom-value"),
						ValueFunc:               func() string { return "custom-value" },
						ExpectedInKubeletLabels: false,
						ExpectedOnNode:          true,
					},
					// kubelet.kubernetes.io is in the kubelet-allowed namespace, so kubelet CAN set these
					{
						Name:                    "kubelet.kubernetes.io (kubelet-allowed)",
						Label:                   "kubelet.kubernetes.io/custom-label",
						SetupFunc:               requireFunc("kubelet.kubernetes.io/custom-label", "custom-value"),
						ValueFunc:               func() string { return "custom-value" },
						ExpectedInKubeletLabels: true,
						ExpectedOnNode:          true,
					},
				}

				It("should support individual instance type labels (when all pods scheduled at once)", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)

					var podDetails []struct {
						pod   *v1.Pod
						entry WellKnownLabelEntry
					}
					for _, item := range entries {
						if item.SetupFunc != nil {
							continue // can't support nonstandard setup here as we're putting all labels on one pod
						}
						podDetails = append(podDetails, struct {
							pod   *v1.Pod
							entry WellKnownLabelEntry
						}{
							pod:   coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: item.ValueFunc()}}),
							entry: item,
						})
					}
					pods := lo.Map(
						podDetails,
						func(detail struct {
							pod   *v1.Pod
							entry WellKnownLabelEntry
						}, _ int) *v1.Pod {
							return detail.pod
						})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pods...)

					// Collect all the VMs we provisioned
					vmInputs := map[string]*fake.VirtualMachineCreateOrUpdateInput{}

					for vmInput := range azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.All() {
						vmInputs[*vmInput.VM.Name] = vmInput
					}

					for _, detail := range podDetails {
						key := lo.Keys(detail.pod.Spec.NodeSelector)[0]
						node := ExpectScheduled(ctx, env.Client, detail.pod)
						if detail.entry.ExpectedOnNode {
							Expect(node.Labels[key]).To(Equal(detail.pod.Spec.NodeSelector[key]))
						} else {
							Expect(node.Labels).ToNot(HaveKey(key))
						}

						// Get the VM creation input and decode custom data
						// Extract the vm name from the provider ID
						vmName, err := nodeclaimutils.GetVMName(node.Spec.ProviderID)
						Expect(err).ToNot(HaveOccurred())

						vm := vmInputs[vmName].VM
						if detail.entry.ExpectedInKubeletLabels {
							ExpectKubeletNodeLabelsInCustomData(&vm, detail.entry.Label, detail.entry.ValueFunc())
						} else {
							ExpectKubeletNodeLabelsNotInCustomData(&vm, detail.entry.Label, detail.entry.ValueFunc())
						}
					}
				})

				DescribeTable(
					"should support individual instance type labels (when all pods scheduled individually)",
					func(item WellKnownLabelEntry) {
						if item.SetupFunc != nil {
							item.SetupFunc()
						}

						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						value := item.ValueFunc()

						pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: value}})
						// Simulate multiple scheduling passes before final binding, this ensures that when real scheduling happens we won't
						// end up with a new node for each scheduling attempt
						if item.Label != v1.LabelWindowsBuild { // TODO: special case right now as we don't support it
							bindings := []Bindings{}
							for range 3 {
								bindings = append(bindings, ExpectProvisionedNoBinding(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, pod))
							}
							for i := range len(bindings) {
								Expect(lo.Values(bindings[i])).ToNot(BeEmpty())
								Expect(lo.Values(bindings[i])[0].Node.Name).To(Equal(lo.Values(bindings[0])[0].Node.Name), "expected all bindings to have the same node name")
							}
						}
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						node := ExpectScheduled(ctx, env.Client, pod)

						if item.ExpectedOnNode {
							Expect(node.Labels[item.Label]).To(Equal(value))
						} else {
							Expect(node.Labels).ToNot(HaveKey(item.Label))
						}

						// Get the VM creation input and decode custom data
						Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
						vmInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
						vm := vmInput.VM
						if item.ExpectedInKubeletLabels {
							ExpectKubeletNodeLabelsInCustomData(&vm, item.Label, value)
						} else {
							ExpectKubeletNodeLabelsNotInCustomData(&vm, item.Label, value)
						}
					},
					lo.Map(entries, func(item WellKnownLabelEntry, _ int) TableEntry {
						return Entry(item.Name, item)
					}),
				)

				DescribeTable(
					"should support individual instance type labels (when all pods scheduled individually) on bootstrap API",
					func(item WellKnownLabelEntry) {
						if item.SetupFunc != nil {
							item.SetupFunc()
						}

						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						value := item.ValueFunc()

						pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: value}})
						// Simulate multiple scheduling passes before final binding, this ensures that when real scheduling happens we won't
						// end up with a new node for each scheduling attempt
						if item.Label != v1.LabelWindowsBuild { // TODO: special case right now as we don't support it
							bindings := []Bindings{}
							for range 3 {
								bindings = append(bindings, ExpectProvisionedNoBinding(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, pod))
							}
							for i := range len(bindings) {
								Expect(lo.Values(bindings[i])).ToNot(BeEmpty())
								Expect(lo.Values(bindings[i])[0].Node.Name).To(Equal(lo.Values(bindings[0])[0].Node.Name), "expected all bindings to have the same node name")
							}
						}
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, azureEnvBootstrap, pod)

						node := ExpectScheduled(ctx, env.Client, pod)

						if item.ExpectedOnNode {
							Expect(node.Labels[item.Label]).To(Equal(value))
						} else {
							Expect(node.Labels).ToNot(HaveKey(item.Label))
						}

						// Get the bootstrap API input
						Expect(azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
						bootstrapInput := azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
						if item.ExpectedInKubeletLabels {
							Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(item.Label, value))
						} else {
							Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).ToNot(HaveKeyWithValue(item.Label, value))
						}
					},
					lo.Map(entries, func(item WellKnownLabelEntry, _ int) TableEntry {
						return Entry(item.Name, item)
					}),
				)

				It("entries should cover every WellKnownLabel", func() {
					expectedLabels := append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)
					Expect(lo.Map(entries, func(item WellKnownLabelEntry, _ int) string { return item.Label })).To(ContainElements(expectedLabels))
				})

				nonSchedulableLabels := map[string]string{
					labels.AKSLabelRole:                     "agent",
					v1beta1.AKSLabelKubeletIdentityClientID: test.Options().KubeletIdentityClientID,
					"kubernetes.azure.com/mode":             "user", // TODO: Will become a WellKnownLabel soon
					//We expect the vnetInfoLabels because we're simulating network plugin Azure by default and they are included there
					labels.AKSLabelSubnetName:          "aks-subnet",
					labels.AKSLabelVNetGUID:            test.Options().VnetGUID,
					labels.AKSLabelAzureCNIOverlay:     strconv.FormatBool(true),
					labels.AKSLabelPodNetworkType:      consts.NetworkPluginModeOverlay,
					karpv1.NodeDoNotSyncTaintsLabelKey: "true",
				}

				It("should write other (non-schedulable) labels to kubelet", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vmInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
					vm := vmInput.VM
					for key, value := range nonSchedulableLabels {
						ExpectKubeletNodeLabelsInCustomData(&vm, key, value)
					}
				})

				DescribeTable("should not write restricted labels to kubelet, but should write allowed labels", func(domain string, allowed bool) {
					nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
						{Key: domain + "/team", Operator: v1.NodeSelectorOpExists},
						{Key: domain + "/custom-label", Operator: v1.NodeSelectorOpExists},
						{Key: "subdomain." + domain + "/custom-label", Operator: v1.NodeSelectorOpExists},
					}

					nodeSelector := map[string]string{
						domain + "/team":                        "team-1",
						domain + "/custom-label":                "custom-value",
						"subdomain." + domain + "/custom-label": "custom-value",
					}

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: nodeSelector})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					node := ExpectScheduled(ctx, env.Client, pod)

					// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vmInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
					vm := vmInput.VM

					// Ensure that the requirements/labels specified above are propagated onto the node and that it didn't do so via kubelet labels
					for k, v := range nodeSelector {
						Expect(node.Labels).To(HaveKeyWithValue(k, v))
						if allowed {
							ExpectKubeletNodeLabelsInCustomData(&vm, k, v)
						} else {
							ExpectKubeletNodeLabelsNotInCustomData(&vm, k, v)
						}
					}
				},
					Entry("node-restriction.kubernetes.io", "node-restriction.kubernetes.io", false),
					Entry("node.kubernetes.io", "node.kubernetes.io", true),
				)

				It("should write other (non-schedulable) labels to kubelet on bootstrap API", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, clusterBootstrap, cloudProviderBootstrap, coreProvisionerBootstrap, azureEnvBootstrap, pod)
					ExpectScheduled(ctx, env.Client, pod)

					// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

					Expect(azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
					bootstrapInput := azureEnvBootstrap.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
					for key, value := range nonSchedulableLabels {
						Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(key, value))
					}
				})

				It("should propagate all values to requirements from skewer", func() {
					var gpuNode *corecloudprovider.InstanceType
					var normalNode *corecloudprovider.InstanceType
					for _, instanceType := range instanceTypes {
						if instanceType.Name == "Standard_D2_v2" {
							normalNode = instanceType
						}
						// #nosec G101
						if instanceType.Name == "Standard_NC24ads_A100_v4" {
							gpuNode = instanceType
						}
					}

					Expect(normalNode.Name).To(Equal("Standard_D2_v2"))
					Expect(gpuNode.Name).To(Equal("Standard_NC24ads_A100_v4"))

					Expect(normalNode.Requirements.Get(v1beta1.LabelSKUName).Values()).To(ConsistOf("Standard_D2_v2"))
					Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUName).Values()).To(ConsistOf("Standard_NC24ads_A100_v4"))

					Expect(normalNode.Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1beta1.HyperVGenerationV1))
					Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1beta1.HyperVGenerationV2))

					Expect(normalNode.Requirements.Get(v1beta1.LabelSKUVersion).Values()).To(ConsistOf("2"))
					Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUVersion).Values()).To(ConsistOf("4"))

					// CPU (requirements and capacity)
					Expect(normalNode.Requirements.Get(v1beta1.LabelSKUCPU).Values()).To(ConsistOf("2"))
					Expect(normalNode.Capacity.Cpu().Value()).To(Equal(int64(2)))
					Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUCPU).Values()).To(ConsistOf("24"))
					Expect(gpuNode.Capacity.Cpu().Value()).To(Equal(int64(24)))

					// Memory (requirements and capacity)
					Expect(normalNode.Requirements.Get(v1beta1.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(7 * 1024))) // 7GiB in MiB
					Expect(normalNode.Capacity.Memory().Value()).To(Equal(int64(7 * 1024 * 1024 * 1024)))                    // 7GiB in bytes
					Expect(gpuNode.Requirements.Get(v1beta1.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(220 * 1024)))  // 220GiB in MiB
					Expect(gpuNode.Capacity.Memory().Value()).To(Equal(int64(220 * 1024 * 1024 * 1024)))                     // 220GiB in bytes

					// GPU -- Number of GPUs
					gpuQuantity, ok := gpuNode.Capacity["nvidia.com/gpu"]
					Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity")
					Expect(gpuQuantity.Value()).To(Equal(int64(1)))

					gpuQuantityNonGPU, ok := normalNode.Capacity["nvidia.com/gpu"]
					Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity, and be zero")
					Expect(gpuQuantityNonGPU.Value()).To(Equal(int64(0)))
				})
			})

			// Ephemeral Disk Placement tests (moved from instancetype/suite_test.go)
			// These test VM-specific DiffDiskSettings.Placement (NVMe vs Cache vs Managed)
			// Mode-specific: DiffDiskSettings.Placement (NVMe/Cache) is a VM StorageProfile concept. Machine API handles placement automatically.
			Context("Ephemeral Disk - Placement (VM-specific)", func() {
				BeforeEach(func() {
					// Enable SIG and repopulate instance types for ephemeral disk tests
					testOptions.UseSIG = true
					ctx = options.ToContext(ctx, testOptions)
					Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
				})
				AfterEach(func() {
					testOptions.UseSIG = false
					ctx = options.ToContext(ctx, testOptions)
					Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
				})
				Context("Placement", func() {
					It("should prefer NVMe disk if supported for ephemeral", func() {
						nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D128ds_v6"},
						})

						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						pod := coretest.UnschedulablePod()
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						ExpectScheduled(ctx, env.Client, pod)

						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm).NotTo(BeNil())
						Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
						Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementNvmeDisk))
					})
					It("should not select NVMe ephemeral disk placement if the sku has an nvme disk, supports ephemeral os disk, but doesnt support NVMe placement", func() {
						nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_NC24ads_A100_v4"},
						})

						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						pod := coretest.UnschedulablePod()
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						ExpectScheduled(ctx, env.Client, pod)

						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm).NotTo(BeNil())
						Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
						Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).ToNot(Equal(armcompute.DiffDiskPlacementNvmeDisk))
					})
					It("should prefer cache disk placement when both cache and temp disk support ephemeral and fit the default 128GB threshold", func() {
						nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D64s_v3"},
						})
						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						pod := coretest.UnschedulablePod()
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						ExpectScheduled(ctx, env.Client, pod)

						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm).NotTo(BeNil())
						Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
						Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementCacheDisk))
					})
					It("should select managed disk if cache disk is too small but temp disk supports ephemeral and fits osDiskSizeGB to have parity with the AKS Nodepool API", func() {
						nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_B20ms"},
						})
						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						pod := coretest.UnschedulablePod()
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						ExpectScheduled(ctx, env.Client, pod)

						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm).NotTo(BeNil())
						Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
					})
				})

				// Shared ephemeral disk provisioning tests moved to pkg/cloudprovider/suite_features_test.go (runSharedEphemeralDiskTests)
				// VM-specific placement and NVMe tests remain here.

				It("should select NvmeDisk for v6 skus with maxNvmeDiskSize > 0", func() {
					nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D128ds_v6"}})
					nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](100)
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm).NotTo(BeNil())

					Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
					Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementNvmeDisk))
				})
			})

			// Instance Types (moved from instancetype/suite_test.go)
			// Mode-specific: VM identity (UserAssignedIdentities), auto-delete (OSDisk/NIC DeleteOption), secondary IPs.
			Context("Instance Types", func() {
				It("should support provisioning with no labels", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
				})
				It("should have VM identity set", func() {
					ctx = options.ToContext(
						ctx,
						test.Options(test.OptionsFields{
							NodeIdentities: []string{
								"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
								"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
							},
						}))

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Identity).ToNot(BeNil())

					Expect(lo.FromPtr(vm.Identity.Type)).To(Equal(armcompute.ResourceIdentityTypeUserAssigned))
					Expect(vm.Identity.UserAssignedIdentities).ToNot(BeNil())
					Expect(vm.Identity.UserAssignedIdentities).To(HaveLen(2))
					Expect(vm.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
					Expect(vm.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2"))
				})
				Context("VM Profile", func() {
					It("should have OS disk and network interface set to auto-delete", func() {
						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						pod := coretest.UnschedulablePod()
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						ExpectScheduled(ctx, env.Client, pod)

						Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm.Properties).ToNot(BeNil())

						Expect(vm.Properties.StorageProfile).ToNot(BeNil())
						Expect(vm.Properties.StorageProfile.OSDisk).ToNot(BeNil())
						osDiskDeleteOption := vm.Properties.StorageProfile.OSDisk.DeleteOption
						Expect(osDiskDeleteOption).ToNot(BeNil())
						Expect(lo.FromPtr(osDiskDeleteOption)).To(Equal(armcompute.DiskDeleteOptionTypesDelete))

						Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())

						for _, nic := range vm.Properties.NetworkProfile.NetworkInterfaces {
							nicDeleteOption := nic.Properties.DeleteOption
							Expect(nicDeleteOption).To(Not(BeNil()))
							Expect(lo.FromPtr(nicDeleteOption)).To(Equal(armcompute.DeleteOptionsDelete))
						}
					})
					It("should not create unneeded secondary ips for azure cni with overlay", func() {
						ExpectApplied(ctx, env.Client, nodePool, nodeClass)
						pod := coretest.UnschedulablePod()
						ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
						ExpectScheduled(ctx, env.Client, pod)

						Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
						vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
						Expect(vm.Properties).ToNot(BeNil())

						Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
						Expect(len(vm.Properties.NetworkProfile.NetworkInterfaces)).To(Equal(1))
						Expect(lo.FromPtr(vm.Properties.NetworkProfile.NetworkInterfaces[0].Properties.Primary)).To(BeTrue())

						Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
						nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
						Expect(nic.Properties).ToNot(BeNil())

						Expect(len(nic.Properties.IPConfigurations)).To(Equal(1))
					})
				})
			})

			// LoadBalancer (moved from instancetype/suite_test.go)
			// Mode-specific: VM NIC backend pool management. Machine API doesn't manage NICs.
			Context("LoadBalancer", func() {
				resourceGroup := "test-resourceGroup"

				It("should include loadbalancer backend pools the allocated VMs", func() {
					standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
					internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

					azureEnv.LoadBalancersAPI.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)
					azureEnv.LoadBalancersAPI.LoadBalancers.Store(lo.FromPtr(internalLB.ID), internalLB)

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					iface := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface

					Expect(iface.Properties.IPConfigurations).ToNot(BeEmpty())
					Expect(lo.FromPtr(iface.Properties.IPConfigurations[0].Properties.Primary)).To(Equal(true))

					backendPools := iface.Properties.IPConfigurations[0].Properties.LoadBalancerBackendAddressPools
					Expect(backendPools).To(HaveLen(3))
					Expect(lo.FromPtr(backendPools[0].ID)).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/kubernetes"))
					Expect(lo.FromPtr(backendPools[1].ID)).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/loadBalancers/kubernetes/backendAddressPools/aksOutboundBackendPool"))
					Expect(lo.FromPtr(backendPools[2].ID)).To(Equal("/subscriptions/subscriptionID/resourceGroups/test-resourceGroup/providers/Microsoft.Network/loadBalancers/kubernetes-internal/backendAddressPools/kubernetes"))
				})
			})

			// KubeletConfig tests (moved from instancetype/suite_test.go)
			// Mode-specific: customData kubelet flags (different abstraction from Machine API's KubeletConfig struct fields).
			Context("Nodepool with KubeletConfig", func() {
				It("should support provisioning with kubeletConfig, computeResources and maxPods not specified", func() {
					nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            lo.ToPtr("static"),
						CPUCFSQuota:                 lo.ToPtr(true),
						CPUCFSQuotaPeriod:           metav1.Duration{},
						ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
						ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
						TopologyManagerPolicy:       lo.ToPtr("best-effort"),
						AllowedUnsafeSysctls:        []string{"Allowed", "Unsafe", "Sysctls"},
						ContainerLogMaxSize:         lo.ToPtr("42Mi"),
						ContainerLogMaxFiles:        lo.ToPtr[int32](13),
						PodPidsLimit:                lo.ToPtr[int64](99),
					}

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					customData := ExpectDecodedCustomData(azureEnv)

					expectedFlags := map[string]string{
						"eviction-hard":           "memory.available<750Mi",
						"image-gc-high-threshold": "30",
						"image-gc-low-threshold":  "20",
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
					Expect(customData).To(SatisfyAny( // AKS default
						ContainSubstring("--system-reserved=cpu=0,memory=0"),
						ContainSubstring("--system-reserved=memory=0,cpu=0"),
					))
					Expect(customData).To(SatisfyAny( // AKS calculation based on cpu and memory
						ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
						ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
					))
				})
			})

			Context("Nodepool with KubeletConfig on a kubenet Cluster", func() {
				var originalOptions *options.Options

				BeforeEach(func() {
					originalOptions = options.FromContext(ctx)
					ctx = options.ToContext(
						ctx,
						test.Options(test.OptionsFields{
							NetworkPlugin: lo.ToPtr("kubenet"),
						}))
				})

				AfterEach(func() {
					ctx = options.ToContext(ctx, originalOptions)
				})
				It("should not include cilium or azure cni vnet labels", func() {
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					customData := ExpectDecodedCustomData(azureEnv)
					// Since the network plugin is not "azure" it should not include the following kubeletLabels
					Expect(customData).To(Not(SatisfyAny(
						ContainSubstring("kubernetes.azure.com/network-subnet=aks-subnet"),
						ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c"),
						ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
					)))
				})
				It("should support provisioning with kubeletConfig, computeResources and maxPods not specified", func() {
					nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            lo.ToPtr("static"),
						CPUCFSQuota:                 lo.ToPtr(true),
						CPUCFSQuotaPeriod:           metav1.Duration{},
						ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
						ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
						TopologyManagerPolicy:       lo.ToPtr("best-effort"),
						AllowedUnsafeSysctls:        []string{"Allowed", "Unsafe", "Sysctls"},
						ContainerLogMaxSize:         lo.ToPtr("42Mi"),
						ContainerLogMaxFiles:        lo.ToPtr[int32](13),
						PodPidsLimit:                lo.ToPtr[int64](99),
					}

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					customData := ExpectDecodedCustomData(azureEnv)
					expectedFlags := map[string]string{
						"eviction-hard":           "memory.available<750Mi",
						"max-pods":                "110",
						"image-gc-low-threshold":  "20",
						"image-gc-high-threshold": "30",
						"cpu-cfs-quota":           "true",
						"topology-manager-policy": "best-effort",
						"container-log-max-size":  "42Mi",
						"allowed-unsafe-sysctls":  "Allowed,Unsafe,Sysctls",
						"cpu-manager-policy":      "static",
						"container-log-max-files": "13",
						"pod-max-pids":            "99",
					}
					ExpectKubeletFlags(azureEnv, customData, expectedFlags)
					Expect(customData).To(SatisfyAny( // AKS default
						ContainSubstring("--system-reserved=cpu=0,memory=0"),
						ContainSubstring("--system-reserved=memory=0,cpu=0"),
					))
					Expect(customData).To(SatisfyAny( // AKS calculation based on cpu and memory
						ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
						ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
					))
				})
				It("should support provisioning with kubeletConfig, computeResources and maxPods specified", func() {
					nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
						CPUManagerPolicy:            lo.ToPtr("static"),
						CPUCFSQuota:                 lo.ToPtr(true),
						CPUCFSQuotaPeriod:           metav1.Duration{},
						ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
						ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
						TopologyManagerPolicy:       lo.ToPtr("best-effort"),
						AllowedUnsafeSysctls:        []string{"Allowed", "Unsafe", "Sysctls"},
						ContainerLogMaxSize:         lo.ToPtr("42Mi"),
						ContainerLogMaxFiles:        lo.ToPtr[int32](13),
						PodPidsLimit:                lo.ToPtr[int64](99),
					}
					nodeClass.Spec.MaxPods = lo.ToPtr(int32(15))

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					customData := ExpectDecodedCustomData(azureEnv)
					expectedFlags := map[string]string{
						"eviction-hard":           "memory.available<750Mi",
						"max-pods":                "15",
						"image-gc-low-threshold":  "20",
						"image-gc-high-threshold": "30",
						"cpu-cfs-quota":           "true",
						"topology-manager-policy": "best-effort",
						"container-log-max-size":  "42Mi",
						"allowed-unsafe-sysctls":  "Allowed,Unsafe,Sysctls",
						"cpu-manager-policy":      "static",
						"container-log-max-files": "13",
						"pod-max-pids":            "99",
					}

					ExpectKubeletFlags(azureEnv, customData, expectedFlags)
					Expect(customData).To(SatisfyAny( // AKS default
						ContainSubstring("--system-reserved=cpu=0,memory=0"),
						ContainSubstring("--system-reserved=memory=0,cpu=0"),
					))
					Expect(customData).To(SatisfyAny( // AKS calculation based on cpu and memory
						ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
						ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
					))
				})
			})
		})
	})

	// Mode-specific: BootstrappingClient is a legacy VM CSE bootstrap mode.
	Context("ProvisionMode = BootstrappingClient", func() {
		var bootstrapEnv *test.Environment
		var bootstrapCloudProvider *CloudProvider
		var bootstrapCluster *state.Cluster
		var bootstrapProvisioner *provisioning.Provisioner

		BeforeEach(func() {
			bootstrapOpts := test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeBootstrappingClient),
			})
			bootstrapCtx := coreoptions.ToContext(ctx, coretest.Options())
			bootstrapCtx = options.ToContext(bootstrapCtx, bootstrapOpts)

			bootstrapEnv = test.NewEnvironment(bootstrapCtx, env)
			test.ApplyDefaultStatus(nodeClass, env, bootstrapOpts.UseSIG)
			bootstrapCloudProvider = New(bootstrapEnv.InstanceTypesProvider, bootstrapEnv.VMInstanceProvider, bootstrapEnv.AKSMachineProvider, recorder, env.Client, bootstrapEnv.ImageProvider, bootstrapEnv.InstanceTypeStore)
			bootstrapCluster = state.NewCluster(fakeClock, env.Client, bootstrapCloudProvider)
			bootstrapProvisioner = provisioning.NewProvisioner(env.Client, recorder, bootstrapCloudProvider, bootstrapCluster, fakeClock)
		})

		AfterEach(func() {
			bootstrapCloudProvider.WaitForInstancePromises()
			bootstrapCluster.Reset()
			bootstrapEnv.Reset(ctx)
		})

		It("should provision the node and CSE", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, bootstrapCluster, bootstrapCloudProvider, bootstrapProvisioner, bootstrapEnv, pod)
			ExpectCSEProvisioned(bootstrapEnv)
			ExpectScheduled(ctx, env.Client, pod)
		})

		It("should not reattempt creation of a vm thats been created before, and also not CSE", func() {
			nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"karpenter.sh/nodepool": nodePool.Name},
				},
				Spec: karpv1.NodeClaimSpec{NodeClassRef: &karpv1.NodeClassReference{Name: nodeClass.Name}},
			})
			vmName := instance.GenerateResourceName(nodeClaim.Name)
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
			bootstrapEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			_, err := bootstrapCloudProvider.Create(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())

			ExpectCSENotProvisioned(bootstrapEnv)
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
