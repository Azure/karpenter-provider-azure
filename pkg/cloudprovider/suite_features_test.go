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
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
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
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/labels"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

func runFeatureTests(provisionMode provisionModeTestCase) {
	Context("Create - GPU Workloads + Nodes", func() {
		It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				vmSize := lo.FromPtr(aksMachine.Properties.Hardware.VMSize)
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeFalse())
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile.VMSize).ToNot(BeNil())
				vmSize := string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeFalse())
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
			Expect(node.Labels).To(HaveKeyWithValue("node.kubernetes.io/instance-type", "Standard_NC16as_T4_v3"))

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				vmSize := lo.FromPtr(aksMachine.Properties.Hardware.VMSize)
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeTrue())
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile.VMSize).ToNot(BeNil())
				vmSize := string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeTrue())
			}
			Expect(node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-name", "T4"))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-manufacturer", v1beta1.ManufacturerNvidia))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "1"))
		})
	})

	Context("Create - Additional Tags", func() {
		It("should add additional tags to the node", func() {
			originalOptions := options.FromContext(ctx)
			updatedOptions := *originalOptions
			updatedOptions.AdditionalTags = map[string]string{"karpenter.azure.com/test-tag": "test-value"}
			ctx = options.ToContext(ctx, &updatedOptions)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine).ToNot(BeNil())
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.azure.com_test-tag", lo.ToPtr("test-value")))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.azure.com_cluster", lo.ToPtr("test-cluster")))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("compute.aks.billing", lo.ToPtr("linux")))
				Expect(aksMachine.Properties.Tags).To(HaveKeyWithValue("karpenter.sh_nodepool", lo.ToPtr(nodePool.Name)))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Tags).To(Equal(map[string]*string{
					"karpenter.azure.com_test-tag": lo.ToPtr("test-value"),
					"karpenter.azure.com_cluster":  lo.ToPtr("test-cluster"),
					"compute.aks.billing":          lo.ToPtr("linux"),
					"karpenter.sh_nodepool":        lo.ToPtr(nodePool.Name),
				}))
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				Expect(nic.Interface.Tags).To(Equal(map[string]*string{
					"karpenter.azure.com_test-tag": lo.ToPtr("test-value"),
					"karpenter.azure.com_cluster":  lo.ToPtr("test-cluster"),
					"compute.aks.billing":          lo.ToPtr("linux"),
					"karpenter.sh_nodepool":        lo.ToPtr(nodePool.Name),
				}))
			}
		})
	})

	Context("Ephemeral Disk", func() {
		var originalOptions *options.Options
		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			updatedOptions := *originalOptions
			updatedOptions.UseSIG = true
			ctx = options.ToContext(ctx, &updatedOptions)
			Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
			Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
		})

		// For Machine API mode, this responsibility is delegated to Machine API.
		// - VMs control detailed StorageProfile, DiffDiskSettings, Placement (NVMe/Cache)
		// - AKS machines use OSDiskType (Managed/Ephemeral) and OSDiskSizeGB
		// - AKS machines automatically handles placement decisions (NVMe vs Cache disk)
		if !provisionMode.isAKSMachineMode() {
			Context("Placement", func() {
				It("should prefer NVMe disk if supported for ephemeral", func() {
					nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D128ds_v6"},
					})
					nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](100)

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
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
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
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
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
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
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm).NotTo(BeNil())
					Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
				})
			})
		}

		It("should fail to provision if ephemeral disk ask for is too large", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1beta1.LabelSKUStorageEphemeralOSMaxSize,
				Operator: v1.NodeSelectorOpGt,
				Values:   []string{"100000"},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
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
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(30)))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(30)))
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
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
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(256)))
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(256)))
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
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
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeManaged))
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(128)))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
			}
		})

		It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
			nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"},
			})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
				Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
			}
		})
	})

	Context("ImageReference", func() {
		It("should use shared image gallery images when options are set to UseSIG", func() {
			imageOptions := *options.FromContext(ctx)
			imageOptions.UseSIG = true
			ctx = options.ToContext(ctx, &imageOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, imageOptions.ParsedDiskEncryptionSetID, imageOptions.NetworkPolicy, imageOptions.NetworkPlugin)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring("AKSUbuntu"))
				Expect(nodeImageVersion).To(MatchRegexp(`^AKSUbuntu-.*-.*$`))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
				Expect(vm.Properties.StorageProfile.ImageReference.ID).ShouldNot(BeNil())
				Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).Should(BeNil())
				Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(imageOptions.SIGSubscriptionID))
				Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring("AKSUbuntu"))
			}
		})

		// For Machine API mode, CIG is not supported (and not possible).
		if !provisionMode.isAKSMachineMode() {
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
		}
	})

	Context("ImageProvider + Image Family", func() {
		DescribeTable("should select the right Shared Image Gallery image for a given instance type",
			func(instanceType string, imageFamily string, expectedImageDefinition string, expectedGalleryRG string, expectedGalleryURL string) {
				imageOptions := *options.FromContext(ctx)
				imageOptions.UseSIG = true
				ctx = options.ToContext(ctx, &imageOptions)
				azureEnv = test.NewEnvironment(ctx, env)
				statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, imageOptions.ParsedDiskEncryptionSetID, imageOptions.NetworkPolicy, imageOptions.NetworkPlugin)
				cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
				cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
				coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

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

				if provisionMode.isAKSMachineMode() {
					Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
					Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())
					nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
					Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
				} else {
					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
					expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s", imageOptions.SIGSubscriptionID, expectedGalleryRG, expectedGalleryURL, expectedImageDefinition)
					Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(expectedPrefix))
				}
			},
			Entry("Gen2, Gen1 instance type with AKSUbuntu image family", "Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
			Entry("Gen1 instance type with AKSUbuntu image family", "Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
			Entry("ARM instance type with AKSUbuntu image family", "Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
		)
		It("should select the right Shared Image Gallery image for a given instance type, Gen2 instance type with AzureLinux image family", func() {
			instanceType := "Standard_D2_v5"
			imageFamily := v1beta1.AzureLinuxImageFamily
			kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
			expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
			expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)
			imageOptions := *options.FromContext(ctx)
			imageOptions.UseSIG = true
			ctx = options.ToContext(ctx, &imageOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, imageOptions.ParsedDiskEncryptionSetID, imageOptions.NetworkPolicy, imageOptions.NetworkPlugin)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

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

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
				expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s", imageOptions.SIGSubscriptionID, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName, expectedImageDefinition)
				Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(expectedPrefix))
			}
		})
		It("should select the right Shared Image Gallery image for a given instance type, Gen1 instance type with AzureLinux image family", func() {
			instanceType := "Standard_D2_v3"
			imageFamily := v1beta1.AzureLinuxImageFamily
			kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
			expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
			expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)
			imageOptions := *options.FromContext(ctx)
			imageOptions.UseSIG = true
			ctx = options.ToContext(ctx, &imageOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, imageOptions.ParsedDiskEncryptionSetID, imageOptions.NetworkPolicy, imageOptions.NetworkPlugin)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

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

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
				expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s", imageOptions.SIGSubscriptionID, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName, expectedImageDefinition)
				Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(expectedPrefix))
			}
		})
		It("should select the right Shared Image Gallery image for a given instance type, ARM instance type with AzureLinux image family", func() {
			instanceType := "Standard_D16plds_v5"
			imageFamily := v1beta1.AzureLinuxImageFamily
			kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
			expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
			expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)
			imageOptions := *options.FromContext(ctx)
			imageOptions.UseSIG = true
			ctx = options.ToContext(ctx, &imageOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, imageOptions.ParsedDiskEncryptionSetID, imageOptions.NetworkPolicy, imageOptions.NetworkPlugin)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

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

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
				expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s", imageOptions.SIGSubscriptionID, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName, expectedImageDefinition)
				Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(expectedPrefix))
			}
		})

		// For Machine API mode, CIG is not supported (and not possible).
		if !provisionMode.isAKSMachineMode() {
			imageDefinition := func(imageDefinition string) func() string {
				return func() string { return imageDefinition }
			}
			azureLinuxGen2ImageDefinition := func() string {
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				return lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)
			}
			azureLinuxGen1ImageDefinition := func() string {
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				return lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)
			}
			azureLinuxGen2ArmImageDefinition := func() string {
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				return lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)
			}

			DescribeTable("should select the right Community Gallery image for a given instance type",
				func(instanceType string, imageFamily string, expectedImageDefinition func() string, expectedGalleryURL string) {
					imageOptions := test.Options(test.OptionsFields{
						UseSIG: lo.ToPtr(false),
					})
					ctx = imageOptions.ToContext(ctx)
					imageStatusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, imageOptions.ParsedDiskEncryptionSetID, imageOptions.NetworkPolicy, imageOptions.NetworkPlugin)

					nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
					coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType}})

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, imageStatusController, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
					Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).ToNot(BeNil())
					parts := strings.Split(*vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID, "/")
					Expect(parts[2]).To(Equal(expectedGalleryURL))
					Expect(parts[4]).To(Equal(expectedImageDefinition()))
				},
				Entry("Gen2, Gen1 instance type with AKSUbuntu image family", "Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imageDefinition(imagefamily.Ubuntu2204Gen2ImageDefinition), imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("Gen1 instance type with AKSUbuntu image family", "Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imageDefinition(imagefamily.Ubuntu2204Gen1ImageDefinition), imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("ARM instance type with AKSUbuntu image family", "Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imageDefinition(imagefamily.Ubuntu2204Gen2ArmImageDefinition), imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("Gen2 instance type with AzureLinux image family", "Standard_D2_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				Entry("Gen1 instance type with AzureLinux image family", "Standard_D2_v3", v1beta1.AzureLinuxImageFamily, azureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				Entry("ARM instance type with AzureLinux image family", "Standard_D16plds_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
			)
		}
	})

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
			ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if !provisionMode.isAKSMachineMode() {
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
				Expect(customData).To(SatisfyAny(
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(customData).To(SatisfyAny(
					ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
					ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
				))
			}
			// For Machine API mode, this responsibility is delegated to Machine API.
		})
	})

	Context("Create - Labels and Taints", func() {
		type wellKnownLabelEntry struct {
			name                    string
			label                   string
			valueFunc               func() string
			setupFunc               func()
			expectedInKubeletLabels bool
			expectedOnNode          bool
		}

		requireFunc := func(key, value string) func() {
			return func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements,
					karpv1.NodeSelectorRequirementWithMinValues{Key: key, Operator: v1.NodeSelectorOpIn, Values: []string{value}},
				)
			}
		}

		entries := []wellKnownLabelEntry{
			{name: v1.LabelTopologyRegion, label: v1.LabelTopologyRegion, valueFunc: func() string { return fake.Region }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: karpv1.NodePoolLabelKey, label: karpv1.NodePoolLabelKey, valueFunc: func() string { return nodePool.Name }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1.LabelTopologyZone, label: v1.LabelTopologyZone, valueFunc: func() string { return fakeZone1 }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1.LabelInstanceTypeStable, label: v1.LabelInstanceTypeStable, valueFunc: func() string { return "Standard_NC24ads_A100_v4" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1.LabelOSStable, label: v1.LabelOSStable, valueFunc: func() string { return "linux" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1.LabelArchStable, label: v1.LabelArchStable, valueFunc: func() string { return "amd64" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: karpv1.CapacityTypeLabelKey, label: karpv1.CapacityTypeLabelKey, valueFunc: func() string { return "on-demand" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelPlacementScope, label: v1beta1.LabelPlacementScope, valueFunc: func() string { return v1beta1.PlacementScopeZonal }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUName, label: v1beta1.LabelSKUName, valueFunc: func() string { return "Standard_NC24ads_A100_v4" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUFamily, label: v1beta1.LabelSKUFamily, valueFunc: func() string { return "N" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUSeries, label: v1beta1.LabelSKUSeries, valueFunc: func() string { return "NCads_v4" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUVersion, label: v1beta1.LabelSKUVersion, valueFunc: func() string { return "4" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUStorageEphemeralOSMaxSize, label: v1beta1.LabelSKUStorageEphemeralOSMaxSize, valueFunc: func() string { return "429" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUAcceleratedNetworking, label: v1beta1.LabelSKUAcceleratedNetworking, valueFunc: func() string { return "true" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUStoragePremiumCapable, label: v1beta1.LabelSKUStoragePremiumCapable, valueFunc: func() string { return "true" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUGPUName, label: v1beta1.LabelSKUGPUName, valueFunc: func() string { return "A100" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUGPUManufacturer, label: v1beta1.LabelSKUGPUManufacturer, valueFunc: func() string { return "nvidia" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUGPUCount, label: v1beta1.LabelSKUGPUCount, valueFunc: func() string { return "1" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUCPU, label: v1beta1.LabelSKUCPU, valueFunc: func() string { return "24" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.LabelSKUMemory, label: v1beta1.LabelSKUMemory, valueFunc: func() string { return "8192" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelCPU, label: v1beta1.AKSLabelCPU, valueFunc: func() string { return "24" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelMemory, label: v1beta1.AKSLabelMemory, valueFunc: func() string { return "8192" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelMode + "=user", label: v1beta1.AKSLabelMode, valueFunc: func() string { return "user" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelMode + "=system", label: v1beta1.AKSLabelMode, valueFunc: func() string { return "system" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelScaleSetPriority + "=regular", label: v1beta1.AKSLabelScaleSetPriority, valueFunc: func() string { return "regular" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelScaleSetPriority + "=spot", label: v1beta1.AKSLabelScaleSetPriority, valueFunc: func() string { return "spot" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelPriority + "=regular", label: v1beta1.AKSLabelPriority, valueFunc: func() string { return "regular" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelPriority + "=spot", label: v1beta1.AKSLabelPriority, valueFunc: func() string { return "spot" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: v1beta1.AKSLabelOSSKU, label: v1beta1.AKSLabelOSSKU, valueFunc: func() string { return "Ubuntu" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{
				name:  v1beta1.AKSLabelFIPSEnabled,
				label: v1beta1.AKSLabelFIPSEnabled,
				setupFunc: func() {
					testOptions.UseSIG = true
					ctx = options.ToContext(ctx, testOptions)
					nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
					nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
					azureEnv = test.NewEnvironment(ctx, env)
					statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
					cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
					cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
					coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
					ExpectApplied(ctx, env.Client, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
					Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
				},
				valueFunc:               func() string { return "true" },
				expectedInKubeletLabels: true,
				expectedOnNode:          true,
			},
			{name: v1.LabelFailureDomainBetaRegion, label: v1.LabelFailureDomainBetaRegion, valueFunc: func() string { return fake.Region }, expectedInKubeletLabels: false, expectedOnNode: false},
			{name: v1.LabelFailureDomainBetaZone, label: v1.LabelFailureDomainBetaZone, valueFunc: func() string { return fakeZone1 }, expectedInKubeletLabels: false, expectedOnNode: false},
			{name: "beta.kubernetes.io/arch", label: "beta.kubernetes.io/arch", valueFunc: func() string { return "amd64" }, expectedInKubeletLabels: false, expectedOnNode: false},
			{name: "beta.kubernetes.io/os", label: "beta.kubernetes.io/os", valueFunc: func() string { return "linux" }, expectedInKubeletLabels: false, expectedOnNode: false},
			{name: v1.LabelInstanceType, label: v1.LabelInstanceType, valueFunc: func() string { return "Standard_NC24ads_A100_v4" }, expectedInKubeletLabels: false, expectedOnNode: false},
			{name: "topology.disk.csi.azure.com/zone", label: "topology.disk.csi.azure.com/zone", valueFunc: func() string { return fakeZone1 }, expectedInKubeletLabels: false, expectedOnNode: false},
			{name: v1.LabelWindowsBuild, label: v1.LabelWindowsBuild, valueFunc: func() string { return "window" }, expectedInKubeletLabels: true, expectedOnNode: false},
			{name: v1beta1.AKSLabelCluster, label: v1beta1.AKSLabelCluster, valueFunc: func() string { return "test-resourceGroup" }, expectedInKubeletLabels: true, expectedOnNode: true},
			{name: "kubernetes.io (previously reserved)", label: "kubernetes.io/custom-label", setupFunc: requireFunc("kubernetes.io/custom-label", "custom-value"), valueFunc: func() string { return "custom-value" }, expectedInKubeletLabels: false, expectedOnNode: true},
			{name: "k8s.io (previously reserved)", label: "k8s.io/custom-label", setupFunc: requireFunc("k8s.io/custom-label", "custom-value"), valueFunc: func() string { return "custom-value" }, expectedInKubeletLabels: false, expectedOnNode: true},
			{name: "kubelet.kubernetes.io (kubelet-allowed)", label: "kubelet.kubernetes.io/custom-label", setupFunc: requireFunc("kubelet.kubernetes.io/custom-label", "custom-value"), valueFunc: func() string { return "custom-value" }, expectedInKubeletLabels: true, expectedOnNode: true},
		}

		nonSchedulableLabels := map[string]string{
			labels.AKSLabelRole:                     "agent",
			v1beta1.AKSLabelKubeletIdentityClientID: test.Options().KubeletIdentityClientID,
			"kubernetes.azure.com/mode":             "user",
			labels.AKSLabelSubnetName:               "aks-subnet",
			labels.AKSLabelVNetGUID:                 test.Options().VnetGUID,
			labels.AKSLabelAzureCNIOverlay:          strconv.FormatBool(true),
			labels.AKSLabelPodNetworkType:           consts.NetworkPluginModeOverlay,
			karpv1.NodeDoNotSyncTaintsLabelKey:      "true",
		}

		It("entries should cover every WellKnownLabel", func() {
			expectedLabels := append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)
			Expect(lo.Map(entries, func(item wellKnownLabelEntry, _ int) string { return item.label })).To(ContainElements(expectedLabels))
		})

		It("should include karpenter.sh/unregistered taint", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
				Expect(aksMachine.Properties.Kubernetes.NodeInitializationTaints).To(ContainElement(lo.ToPtr(karpv1.UnregisteredNoExecuteTaint.ToString())))
			} else {
				customData := ExpectDecodedCustomData(azureEnv)
				kubeletFlags := customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
				Expect(kubeletFlags).To(ContainSubstring("--register-with-taints=" + karpv1.UnregisteredNoExecuteTaint.ToString()))
			}
		})

		It("should support individual instance type labels when all pods schedule at once", func() {
			allAtOnceEntries := lo.Filter(entries, func(item wellKnownLabelEntry, _ int) bool {
				return item.setupFunc == nil
			})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			var podDetails []struct {
				pod   *v1.Pod
				entry wellKnownLabelEntry
			}
			for _, item := range allAtOnceEntries {
				podDetails = append(podDetails, struct {
					pod   *v1.Pod
					entry wellKnownLabelEntry
				}{
					pod:   coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.label: item.valueFunc()}}),
					entry: item,
				})
			}
			pods := lo.Map(podDetails, func(detail struct {
				pod   *v1.Pod
				entry wellKnownLabelEntry
			}, _ int) *v1.Pod {
				return detail.pod
			})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pods...)

			vmInputs := map[string]*fake.VirtualMachineCreateOrUpdateInput{}
			if !provisionMode.isAKSMachineMode() {
				for vmInput := range azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.All() {
					vmInputs[*vmInput.VM.Name] = vmInput
				}
			}

			for _, detail := range podDetails {
				key := lo.Keys(detail.pod.Spec.NodeSelector)[0]
				node := ExpectScheduled(ctx, env.Client, detail.pod)
				if detail.entry.expectedOnNode {
					Expect(node.Labels[key]).To(Equal(detail.pod.Spec.NodeSelector[key]))
				} else {
					Expect(node.Labels).ToNot(HaveKey(key))
				}

				if provisionMode.isAKSMachineMode() {
					vmName, err := nodeclaimutils.GetVMName(node.Spec.ProviderID)
					Expect(err).ToNot(HaveOccurred())
					aksMachineName, err := instance.GetAKSMachineNameFromVMName(testOptions.AKSMachinesPoolName, vmName)
					Expect(err).ToNot(HaveOccurred())
					machineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, aksMachineName)
					aksMachine, ok := azureEnv.AKSDataStorage.AKSMachines.Load(machineID)
					Expect(ok).To(BeTrue())
					Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
					if detail.entry.label == v1beta1.AKSLabelFIPSEnabled {
						// Machine API takes responsibility for populating the FIPS label on the Node via kubelet.
						continue
					}
					if v1beta1.IsAKSLabel(detail.entry.label) || labels.IsLabelKubeletManaged(detail.entry.label) || !labels.CanKubeletSetLabel(detail.entry.label) {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(detail.entry.label))
						continue
					}
					if detail.entry.expectedInKubeletLabels {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).To(HaveKeyWithValue(detail.entry.label, lo.ToPtr(detail.entry.valueFunc())))
					} else {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(detail.entry.label))
					}
				} else {
					vmName, err := nodeclaimutils.GetVMName(node.Spec.ProviderID)
					Expect(err).ToNot(HaveOccurred())
					vm := vmInputs[vmName].VM
					Expect(vm.Properties).ToNot(BeNil())
					Expect(vm.Properties.OSProfile).ToNot(BeNil())
					Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

					decodedBytes, err := base64.StdEncoding.DecodeString(*vm.Properties.OSProfile.CustomData)
					Expect(err).To(Succeed())
					decodedString := string(decodedBytes[:])
					startIdx := strings.Index(decodedString, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
					endIdx := strings.Index(decodedString[startIdx:], "\n")
					kubeletNodeLabels := decodedString[startIdx:]
					if endIdx != -1 {
						kubeletNodeLabels = decodedString[startIdx : startIdx+endIdx]
					}
					expectedLabel := fmt.Sprintf("%s=%s", detail.entry.label, detail.entry.valueFunc())
					if detail.entry.expectedInKubeletLabels {
						Expect(kubeletNodeLabels).To(ContainSubstring(expectedLabel))
					} else {
						Expect(kubeletNodeLabels).ToNot(ContainSubstring(expectedLabel))
					}
				}
			}
		})

		DescribeTable(
			"should support individual instance type labels (when all pods scheduled individually)",
			func(item wellKnownLabelEntry) {
				if item.setupFunc != nil {
					item.setupFunc()
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				value := item.valueFunc()
				pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.label: value}})

				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				if item.expectedOnNode {
					Expect(node.Labels[item.label]).To(Equal(value))
				} else {
					Expect(node.Labels).ToNot(HaveKey(item.label))
				}

				if provisionMode.isAKSMachineMode() {
					Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
					Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
					if item.label == v1beta1.AKSLabelFIPSEnabled {
						// Machine API takes responsibility for populating the FIPS label on the Node via kubelet.
						return
					}
					if v1beta1.IsAKSLabel(item.label) || labels.IsLabelKubeletManaged(item.label) || !labels.CanKubeletSetLabel(item.label) {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(item.label))
						return
					}
					if item.expectedInKubeletLabels {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).To(HaveKeyWithValue(item.label, lo.ToPtr(value)))
					} else {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(item.label))
					}
				} else {
					Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Properties).ToNot(BeNil())
					Expect(vm.Properties.OSProfile).ToNot(BeNil())
					Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

					decodedBytes, err := base64.StdEncoding.DecodeString(*vm.Properties.OSProfile.CustomData)
					Expect(err).To(Succeed())
					decodedString := string(decodedBytes[:])
					startIdx := strings.Index(decodedString, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
					endIdx := strings.Index(decodedString[startIdx:], "\n")
					kubeletNodeLabels := decodedString[startIdx:]
					if endIdx != -1 {
						kubeletNodeLabels = decodedString[startIdx : startIdx+endIdx]
					}
					expectedLabel := fmt.Sprintf("%s=%s", item.label, value)
					if item.expectedInKubeletLabels {
						Expect(kubeletNodeLabels).To(ContainSubstring(expectedLabel))
					} else {
						Expect(kubeletNodeLabels).ToNot(ContainSubstring(expectedLabel))
					}
				}
			},
			lo.Map(entries, func(item wellKnownLabelEntry, _ int) TableEntry {
				return Entry(item.name, item)
			}),
		)

		It("should write other (non-schedulable) labels to kubelet", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
				// Machine API owns these AKS/kubelet-managed labels and carries node mode as a first-class field, not as custom NodeLabels.
				for key := range nonSchedulableLabels {
					Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(key))
				}
				Expect(aksMachine.Properties.Mode).ToNot(BeNil())
				Expect(*aksMachine.Properties.Mode).To(Equal(armcontainerservice.AgentPoolModeUser))
			} else {
				customData := ExpectDecodedCustomData(azureEnv)
				startIdx := strings.Index(customData, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
				endIdx := strings.Index(customData[startIdx:], "\n")
				kubeletNodeLabels := customData[startIdx:]
				if endIdx != -1 {
					kubeletNodeLabels = customData[startIdx : startIdx+endIdx]
				}
				for key, value := range nonSchedulableLabels {
					Expect(kubeletNodeLabels).To(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
				}
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

			for key, value := range nodeSelector {
				Expect(node.Labels).To(HaveKeyWithValue(key, value))
			}

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.Kubernetes).ToNot(BeNil())
				for key, value := range nodeSelector {
					if allowed {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).To(HaveKeyWithValue(key, lo.ToPtr(value)))
					} else {
						Expect(aksMachine.Properties.Kubernetes.NodeLabels).ToNot(HaveKey(key))
					}
				}
			} else {
				customData := ExpectDecodedCustomData(azureEnv)
				startIdx := strings.Index(customData, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
				endIdx := strings.Index(customData[startIdx:], "\n")
				kubeletNodeLabels := customData[startIdx:]
				if endIdx != -1 {
					kubeletNodeLabels = customData[startIdx : startIdx+endIdx]
				}
				for key, value := range nodeSelector {
					expectedLabel := fmt.Sprintf("%s=%s", key, value)
					if allowed {
						Expect(kubeletNodeLabels).To(ContainSubstring(expectedLabel))
					} else {
						Expect(kubeletNodeLabels).ToNot(ContainSubstring(expectedLabel))
					}
				}
			}
		},
			Entry("node-restriction.kubernetes.io", "node-restriction.kubernetes.io", false),
			Entry("node.kubernetes.io", "node.kubernetes.io", true),
		)
	})

	// For Machine API mode, these responsibilities are delegated to Machine API.
	if !provisionMode.isAKSMachineMode() {
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

		Context("Create - Subnet", func() {
			It("should use the VNET_SUBNET_ID", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet"))
			})

			It("should use the subnet specified in the nodeclass", func() {
				clusterSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet/subnets/cluster-subnet"
				nodeClassSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet/subnets/nodeclass-subnet"
				subnetOptions := *options.FromContext(ctx)
				subnetOptions.SubnetID = clusterSubnetID
				ctx = options.ToContext(ctx, &subnetOptions)
				nodeClass.Spec.VNETSubnetID = lo.ToPtr(nodeClassSubnetID)

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal(nodeClassSubnetID))
			})

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
		})

		Context("Create - Load Balancer", func() {
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

		Context("Kubenet", func() {
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
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
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
				Expect(customData).To(SatisfyAny(
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(customData).To(SatisfyAny(
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
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
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
				Expect(customData).To(SatisfyAny(
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(customData).To(SatisfyAny(
					ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
					ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
				))
			})
		})

		Context("Create - VM Identity", func() {
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
		})

		Context("Create - VM Profile", func() {
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

		Context("Create - MISC Bootstrap", func() {
			It("should include or exclude --keep-terminated-pod-volumes based on kubelet version", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				customData := ExpectDecodedCustomData(azureEnv)
				kubeletFlags := customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]

				k8sVersion, err := azureEnv.KubernetesVersionProvider.KubeServerVersion(ctx)
				Expect(err).To(BeNil())
				minorVersion := semver.MustParse(k8sVersion).Minor

				if minorVersion < 31 {
					Expect(kubeletFlags).To(ContainSubstring("--keep-terminated-pod-volumes"))
				} else {
					Expect(kubeletFlags).ToNot(ContainSubstring("--keep-terminated-pod-volumes"))
				}
			})

			It("should include correct flags and credential provider URL when CredentialProviderURL is not empty", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				customData := ExpectDecodedCustomData(azureEnv)
				kubeletFlags := customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]

				k8sVersion, err := azureEnv.KubernetesVersionProvider.KubeServerVersion(ctx)
				Expect(err).To(BeNil())
				credentialProviderURL := bootstrap.CredentialProviderURL(k8sVersion, "amd64")

				if credentialProviderURL != "" {
					Expect(kubeletFlags).ToNot(ContainSubstring("--azure-container-registry-config"))
					Expect(kubeletFlags).To(ContainSubstring("--image-credential-provider-config=/var/lib/kubelet/credential-provider-config.yaml"))
					Expect(kubeletFlags).To(ContainSubstring("--image-credential-provider-bin-dir=/var/lib/kubelet/credential-provider"))
					Expect(customData).To(ContainSubstring(credentialProviderURL))
				}
			})

			It("should include correct flags when CredentialProviderURL is empty", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				customData := ExpectDecodedCustomData(azureEnv)
				kubeletFlags := customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]

				k8sVersion, err := azureEnv.KubernetesVersionProvider.KubeServerVersion(ctx)
				Expect(err).To(BeNil())
				credentialProviderURL := bootstrap.CredentialProviderURL(k8sVersion, "amd64")

				if credentialProviderURL == "" {
					Expect(kubeletFlags).To(ContainSubstring("--azure-container-registry-config"))
					Expect(kubeletFlags).ToNot(ContainSubstring("--image-credential-provider-config"))
					Expect(kubeletFlags).ToNot(ContainSubstring("--image-credential-provider-bin-dir"))
				}
			})
		})
	}

	// For Scriptless mode, these are not supported.
	// Bootstrappingclient mode support some these, in fact, but not investing in its coverage yet due to deprecation.
	if provisionMode.isAKSMachineMode() {
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

			It("should rewrite Preferred to Required on the wire when Status.LocalDNSState=Enabled", func() {
				// Preferred is never sent downstream — Karpenter is the only kube-aware
				// resolver, so ResolvedLocalDNSForWire rewrites Mode to the terminal
				// value implied by Status.LocalDNSState. Enabled => Required.
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
				// Defense-in-depth: if Status hasn't been resolved yet, never pass
				// Preferred downstream — the downstream resolver cannot see cluster
				// gates and would re-decide incorrectly. Fall back to Disabled.
				nodeClass.Spec.LocalDNS = &v1beta1.LocalDNS{
					Mode:             v1beta1.LocalDNSModePreferred,
					VnetDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationVnetDNS),
					KubeDNSOverrides: validLocalDNSOverridePair(v1beta1.LocalDNSForwardDestinationClusterCoreDNS),
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				// The status sub-reconciler resolves Preferred to Enabled in this
				// test env (no cluster conflicts). Wipe LocalDNSState back to nil
				// via a status Patch to drive the "Status not yet resolved"
				// branch of ResolvedLocalDNSForWire. Re-fetch first because the
				// reconcile bumped the resource version.
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

	}
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = BootstrappingClient", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeBootstrappingClient),
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

		// Just for this mode, the coverage is currently unique.
		// Possible to try to reunify them still. But may not worth it given the deprecation (and migration to Machine API).
		Context("Create - Bootstrap", func() {
			type wellKnownLabelEntry struct {
				name                    string
				label                   string
				valueFunc               func() string
				setupFunc               func()
				expectedInKubeletLabels bool
				expectedOnNode          bool
			}

			requireFunc := func(key, value string) func() {
				return func() {
					nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements,
						karpv1.NodeSelectorRequirementWithMinValues{Key: key, Operator: v1.NodeSelectorOpIn, Values: []string{value}},
					)
				}
			}

			entries := []wellKnownLabelEntry{
				{name: v1.LabelTopologyRegion, label: v1.LabelTopologyRegion, valueFunc: func() string { return fake.Region }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: karpv1.NodePoolLabelKey, label: karpv1.NodePoolLabelKey, valueFunc: func() string { return nodePool.Name }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1.LabelTopologyZone, label: v1.LabelTopologyZone, valueFunc: func() string { return fakeZone1 }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1.LabelInstanceTypeStable, label: v1.LabelInstanceTypeStable, valueFunc: func() string { return "Standard_NC24ads_A100_v4" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1.LabelOSStable, label: v1.LabelOSStable, valueFunc: func() string { return "linux" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1.LabelArchStable, label: v1.LabelArchStable, valueFunc: func() string { return "amd64" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: karpv1.CapacityTypeLabelKey, label: karpv1.CapacityTypeLabelKey, valueFunc: func() string { return "on-demand" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelPlacementScope, label: v1beta1.LabelPlacementScope, valueFunc: func() string { return v1beta1.PlacementScopeZonal }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUName, label: v1beta1.LabelSKUName, valueFunc: func() string { return "Standard_NC24ads_A100_v4" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUFamily, label: v1beta1.LabelSKUFamily, valueFunc: func() string { return "N" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUSeries, label: v1beta1.LabelSKUSeries, valueFunc: func() string { return "NCads_v4" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUVersion, label: v1beta1.LabelSKUVersion, valueFunc: func() string { return "4" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUStorageEphemeralOSMaxSize, label: v1beta1.LabelSKUStorageEphemeralOSMaxSize, valueFunc: func() string { return "429" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUAcceleratedNetworking, label: v1beta1.LabelSKUAcceleratedNetworking, valueFunc: func() string { return "true" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUStoragePremiumCapable, label: v1beta1.LabelSKUStoragePremiumCapable, valueFunc: func() string { return "true" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUGPUName, label: v1beta1.LabelSKUGPUName, valueFunc: func() string { return "A100" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUGPUManufacturer, label: v1beta1.LabelSKUGPUManufacturer, valueFunc: func() string { return "nvidia" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUGPUCount, label: v1beta1.LabelSKUGPUCount, valueFunc: func() string { return "1" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUCPU, label: v1beta1.LabelSKUCPU, valueFunc: func() string { return "24" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.LabelSKUMemory, label: v1beta1.LabelSKUMemory, valueFunc: func() string { return "8192" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelCPU, label: v1beta1.AKSLabelCPU, valueFunc: func() string { return "24" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelMemory, label: v1beta1.AKSLabelMemory, valueFunc: func() string { return "8192" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelMode + "=user", label: v1beta1.AKSLabelMode, valueFunc: func() string { return "user" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelMode + "=system", label: v1beta1.AKSLabelMode, valueFunc: func() string { return "system" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelScaleSetPriority + "=regular", label: v1beta1.AKSLabelScaleSetPriority, valueFunc: func() string { return "regular" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelScaleSetPriority + "=spot", label: v1beta1.AKSLabelScaleSetPriority, valueFunc: func() string { return "spot" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelPriority + "=regular", label: v1beta1.AKSLabelPriority, valueFunc: func() string { return "regular" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelPriority + "=spot", label: v1beta1.AKSLabelPriority, valueFunc: func() string { return "spot" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: v1beta1.AKSLabelOSSKU, label: v1beta1.AKSLabelOSSKU, valueFunc: func() string { return "Ubuntu" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{
					name:  v1beta1.AKSLabelFIPSEnabled,
					label: v1beta1.AKSLabelFIPSEnabled,
					setupFunc: func() {
						testOptions.UseSIG = true
						ctx = options.ToContext(ctx, testOptions)
						nodeClass.Spec.FIPSMode = &v1beta1.FIPSModeFIPS
						nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.AzureLinuxImageFamily)
						azureEnv = test.NewEnvironment(ctx, env)
						statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, azureEnv.DynamicInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
						cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
						cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
						coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
						ExpectApplied(ctx, env.Client, nodeClass)
						ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
						Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), nodeClass)).To(Succeed())
					},
					valueFunc:               func() string { return "true" },
					expectedInKubeletLabels: true,
					expectedOnNode:          true,
				},
				{name: v1.LabelFailureDomainBetaRegion, label: v1.LabelFailureDomainBetaRegion, valueFunc: func() string { return fake.Region }, expectedInKubeletLabels: false, expectedOnNode: false},
				{name: v1.LabelFailureDomainBetaZone, label: v1.LabelFailureDomainBetaZone, valueFunc: func() string { return fakeZone1 }, expectedInKubeletLabels: false, expectedOnNode: false},
				{name: "beta.kubernetes.io/arch", label: "beta.kubernetes.io/arch", valueFunc: func() string { return "amd64" }, expectedInKubeletLabels: false, expectedOnNode: false},
				{name: "beta.kubernetes.io/os", label: "beta.kubernetes.io/os", valueFunc: func() string { return "linux" }, expectedInKubeletLabels: false, expectedOnNode: false},
				{name: v1.LabelInstanceType, label: v1.LabelInstanceType, valueFunc: func() string { return "Standard_NC24ads_A100_v4" }, expectedInKubeletLabels: false, expectedOnNode: false},
				{name: "topology.disk.csi.azure.com/zone", label: "topology.disk.csi.azure.com/zone", valueFunc: func() string { return fakeZone1 }, expectedInKubeletLabels: false, expectedOnNode: false},
				{name: v1.LabelWindowsBuild, label: v1.LabelWindowsBuild, valueFunc: func() string { return "window" }, expectedInKubeletLabels: true, expectedOnNode: false},
				{name: v1beta1.AKSLabelCluster, label: v1beta1.AKSLabelCluster, valueFunc: func() string { return "test-resourceGroup" }, expectedInKubeletLabels: true, expectedOnNode: true},
				{name: "kubernetes.io (previously reserved)", label: "kubernetes.io/custom-label", setupFunc: requireFunc("kubernetes.io/custom-label", "custom-value"), valueFunc: func() string { return "custom-value" }, expectedInKubeletLabels: false, expectedOnNode: true},
				{name: "k8s.io (previously reserved)", label: "k8s.io/custom-label", setupFunc: requireFunc("k8s.io/custom-label", "custom-value"), valueFunc: func() string { return "custom-value" }, expectedInKubeletLabels: false, expectedOnNode: true},
				{name: "kubelet.kubernetes.io (kubelet-allowed)", label: "kubelet.kubernetes.io/custom-label", setupFunc: requireFunc("kubelet.kubernetes.io/custom-label", "custom-value"), valueFunc: func() string { return "custom-value" }, expectedInKubeletLabels: true, expectedOnNode: true},
			}

			It("entries should cover every WellKnownLabel", func() {
				expectedLabels := append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)
				Expect(lo.Map(entries, func(item wellKnownLabelEntry, _ int) string { return item.label })).To(ContainElements(expectedLabels))
			})

			nonSchedulableLabels := map[string]string{
				labels.AKSLabelRole:                     "agent",
				v1beta1.AKSLabelKubeletIdentityClientID: test.Options().KubeletIdentityClientID,
				"kubernetes.azure.com/mode":             "user",
				labels.AKSLabelSubnetName:               "aks-subnet",
				labels.AKSLabelVNetGUID:                 test.Options().VnetGUID,
				labels.AKSLabelAzureCNIOverlay:          strconv.FormatBool(true),
				labels.AKSLabelPodNetworkType:           consts.NetworkPluginModeOverlay,
				karpv1.NodeDoNotSyncTaintsLabelKey:      "true",
			}

			It("should provision the node and CSE", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectCSEProvisioned(azureEnv)
				ExpectScheduled(ctx, env.Client, pod)
			})

			DescribeTable(
				"should support individual instance type labels (when all pods scheduled individually) on bootstrap API",
				func(item wellKnownLabelEntry) {
					if item.setupFunc != nil {
						item.setupFunc()
					}

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					value := item.valueFunc()
					pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.label: value}})
					if item.label != v1.LabelWindowsBuild {
						bindings := []Bindings{}
						for range 3 {
							bindings = append(bindings, ExpectProvisionedNoBinding(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod))
						}
						for i := range len(bindings) {
							Expect(lo.Values(bindings[i])).ToNot(BeEmpty())
							Expect(lo.Values(bindings[i])[0].Node.Name).To(Equal(lo.Values(bindings[0])[0].Node.Name), "expected all bindings to have the same node name")
						}
					}
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					node := ExpectScheduled(ctx, env.Client, pod)

					if item.expectedOnNode {
						Expect(node.Labels[item.label]).To(Equal(value))
					} else {
						Expect(node.Labels).ToNot(HaveKey(item.label))
					}

					Expect(azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
					bootstrapInput := azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
					if item.expectedInKubeletLabels {
						Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(item.label, value))
					} else {
						Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).ToNot(HaveKeyWithValue(item.label, value))
					}
				},
				lo.Map(entries, func(item wellKnownLabelEntry, _ int) TableEntry {
					return Entry(item.name, item)
				}),
			)

			It("should write other (non-schedulable) labels to kubelet on bootstrap API", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
				bootstrapInput := azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
				for key, value := range nonSchedulableLabels {
					Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(key, value))
				}
			})

			It("should not reattempt creation of a vm thats been created before, and also not CSE", func() {
				nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
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
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				_, err := cloudProvider.Create(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())

				ExpectCSENotProvisioned(azureEnv)
			})
		})
	})

	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = false", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless),
				ManageExistingAKSMachines: lo.ToPtr(false),
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

		runFeatureTests(aksscriptlessProvisionMode())
	})

	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = true", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless),
				ManageExistingAKSMachines: lo.ToPtr(true),
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

		runFeatureTests(aksscriptlessProvisionMode())
	})

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

		runFeatureTests(aksMachineAPIHeaderBatchProvisionMode())
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
