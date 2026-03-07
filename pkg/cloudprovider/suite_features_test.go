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
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/awslabs/operatorpkg/object"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"
)

func runCommonGPUTests() {
	Context("Create - GPU Workloads + Nodes", func() {
		It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()
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

			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			// the following checks assume Standard_NC16as_T4_v3 (surprisingly the cheapest GPU in the test set), so test the assumption
			Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_NC16as_T4_v3"))

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()
			Expect(utils.IsNvidiaEnabledSKU(result.vmSize)).To(BeTrue())

			// Verify that the node the pod was scheduled on has GPU resource and labels set
			Expect(node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-name", "T4"))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-manufacturer", v1beta1.ManufacturerNvidia))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "1"))

			// VM mode: also verify GPU-related settings in customData/bootstrap
			if isVMMode() {
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

func runCommonEphemeralDiskTests() {
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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()
			Expect(result.isEphemeral).To(BeTrue())
			Expect(result.diskSizeGB).ToNot(BeNil())
			Expect(*result.diskSizeGB).To(Equal(int32(128))) // Default 128GB minimum

			// VM mode: verify DiffDiskSettings.Option is Local
			if isVMMode() {
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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()
			Expect(result.isEphemeral).To(BeTrue())
			Expect(result.diskSizeGB).ToNot(BeNil())
			Expect(*result.diskSizeGB).To(Equal(int32(30)))

			// VM mode: verify DiffDiskSettings.Option is Local
			if isVMMode() {
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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()
			Expect(result.isEphemeral).To(BeTrue())
			Expect(result.diskSizeGB).ToNot(BeNil())
			Expect(*result.diskSizeGB).To(Equal(int32(256)))

			// VM mode: verify DiffDiskSettings.Option is Local
			if isVMMode() {
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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()
			Expect(result.isEphemeral).To(BeFalse())
			Expect(result.diskSizeGB).ToNot(BeNil())
			Expect(*result.diskSizeGB).To(Equal(int32(128))) // Default size
			// AKS Machine mode: verify OSDiskType is explicitly Managed (preserved from original)
			if isAKSMachineMode() {
				Expect(result.osDiskType).To(Equal("Managed"))
			}
		})
	})
}

func runCommonAdditionalTagsTests() {
	Context("Create - Additional Tags", func() {
		It("should add additional tags to the created resource", func() {
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
				ProvisionMode: lo.Ternary(isAKSMachineMode(), lo.ToPtr(consts.ProvisionModeAKSMachineAPI), nil),
				UseSIG:        lo.ToPtr(true),
				AdditionalTags: map[string]string{
					"karpenter.azure.com/test-tag": "test-value",
				},
			}))

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(getCreateCallCount()).To(Equal(1))
			result := popCreationResult()

			// Common tag assertions — both modes should have these
			Expect(result.tags).To(HaveKey("karpenter.azure.com_test-tag"))
			Expect(*result.tags["karpenter.azure.com_test-tag"]).To(Equal("test-value"))
			Expect(result.tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(*result.tags["karpenter.azure.com_cluster"]).To(Equal("test-cluster"))
			Expect(result.tags).To(HaveKey("compute.aks.billing"))
			Expect(*result.tags["compute.aks.billing"]).To(Equal("linux"))
			Expect(result.tags).To(HaveKey("karpenter.sh_nodepool"))
			Expect(*result.tags["karpenter.sh_nodepool"]).To(Equal(nodePool.Name))

			if isVMMode() {
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				Expect(nic.Interface.Tags).To(HaveKey("karpenter.azure.com_test-tag"))
				Expect(*nic.Interface.Tags["karpenter.azure.com_test-tag"]).To(Equal("test-value"))
				Expect(nic.Interface.Tags).To(HaveKey("karpenter.azure.com_cluster"))
				Expect(*nic.Interface.Tags["karpenter.azure.com_cluster"]).To(Equal("test-cluster"))
				Expect(nic.Interface.Tags).To(HaveKey("compute.aks.billing"))
				Expect(*nic.Interface.Tags["compute.aks.billing"]).To(Equal("linux"))
				Expect(nic.Interface.Tags).To(HaveKey("karpenter.sh_nodepool"))
				Expect(*nic.Interface.Tags["karpenter.sh_nodepool"]).To(Equal(nodePool.Name))
			} else {
				Expect(result.tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
				Expect(result.tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			}
		})
	})
}

func runCommonSubnetTests() {
	Context("Create - Subnet Selection", func() {
		It("should use the subnet specified in the nodeclass", func() {
			// BYO VNet pattern: the subnet reconciler requires custom subnets to be in the
			// same non-managed VNet as the cluster subnet. Both VM and AKS Machine modes
			// need this because setupProvisionModeAKSScriptlessTestEnvironment/setupProvisionModeAKSMachineAPITestEnvironment call ExpectObjectReconciled.
			byoClusterSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/cluster-subnet"
			testSubnetID := "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-resourceGroup/providers/Microsoft.Network/virtualNetworks/byo-vnet-customname/subnets/nodeclassSubnet"

			var byoOpts *options.Options
			if isVMMode() {
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
			}
			byoCtx := options.ToContext(ctx, byoOpts)

			nodeClass.Spec.VNETSubnetID = lo.ToPtr(testSubnetID)
			ExpectApplied(byoCtx, env.Client, nodePool, nodeClass)
			ExpectObjectReconciled(byoCtx, env.Client, statusController, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(byoCtx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(byoCtx, env.Client, pod)
			Expect(getSubnetID()).To(Equal(testSubnetID))
		})
	})
}

func runCommonKubeletConfigTests() {
	Context("Create - KubeletConfig", func() {
		It("should support provisioning with kubeletConfig", func() {
			// Use mode-specific GC thresholds to match original tests:
			// VM: 30/20 (original VM kubelet test), AKS Machine: 85/80 (original AKS Machine test)
			gcHigh := int32(30)
			gcLow := int32(20)
			if isAKSMachineMode() {
				gcHigh = int32(85)
				gcLow = int32(80)
			}

			nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
				CPUManagerPolicy:            lo.ToPtr("static"),
				CPUCFSQuota:                 lo.ToPtr(true),
				CPUCFSQuotaPeriod:           metav1.Duration{},
				ImageGCHighThresholdPercent: lo.ToPtr(gcHigh),
				ImageGCLowThresholdPercent:  lo.ToPtr(gcLow),
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

			if isVMMode() {
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

// runCommonReuseExistingResourceTests was removed — the two branches had 100% mode-split
// code with zero shared logic. They are now inlined at each call site below.

var _ = Describe("CloudProvider - Features", func() {

	Context("ProvisionMode = AKSMachineAPI", func() {
		BeforeEach(func() { setupProvisionModeAKSMachineAPITestEnvironment() })
		AfterEach(func() { teardownTestEnvironment() })

		runCommonGPUTests()
		runCommonEphemeralDiskTests()
		runCommonAdditionalTagsTests()
		runCommonSubnetTests()
		runCommonKubeletConfigTests()

		Context("Create - Reuse Existing Resource (AKS Machine)", func() {
			It("should not reattempt creation of a resource that has been created before", func() {
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
				createdFirst, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, firstNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(createdFirst).ToNot(BeNil())

				// Create again with the same claim — should reuse
				conflictedNodeClaim := firstNodeClaim.DeepCopy()
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
				reusedClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(reusedClaim).ToNot(BeNil())

				// Verify reuse: no new CreateOrUpdate, just a Get
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			})
		})

		// === AKS Machine API Only Tests ===

		Context("Create - Image Selection (SIG)", func() {
			It("should use shared image gallery images", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(getCreateCallCount()).To(Equal(1))
				result := popCreationResult()
				Expect(result.imageRef).To(ContainSubstring("AKSUbuntu"))
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
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					Expect(getCreateCallCount()).To(Equal(1))
					result := popCreationResult()
					Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(getCreateCallCount()).To(Equal(1))
				result := popCreationResult()
				Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(getCreateCallCount()).To(Equal(1))
				result := popCreationResult()
				Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(getCreateCallCount()).To(Equal(1))
				result := popCreationResult()
				Expect(result.imageRef).To(ContainSubstring(expectedImageDefinition))
			})
		})

		Context("Create - Additional Configurations", func() {
			It("should handle configured NodeClass", func() {
				// Comprehensive integration test: exercises kubelet config + image family + BYO VNet subnet +
				// custom tags + Karpenter tags + OS disk size + GPU selection + image version, all at once.
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            lo.ToPtr("static"),
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
				_, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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

				Expect(aksMachine.Properties.Security).ToNot(BeNil())
				Expect(aksMachine.Properties.Security.EnableEncryptionAtHost).ToNot(BeNil())
				Expect(lo.FromPtr(aksMachine.Properties.Security.EnableEncryptionAtHost)).To(BeFalse())
			})
		})
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() { setupProvisionModeAKSScriptlessTestEnvironment() })
		AfterEach(func() { teardownTestEnvironment() })

		runCommonGPUTests()
		runCommonEphemeralDiskTests()
		runCommonAdditionalTagsTests()
		runCommonSubnetTests()
		runCommonKubeletConfigTests()

		Context("Create - Reuse Existing Resource (VM)", func() {
			It("should not reattempt creation of a vm thats been created before", func() {
				// VM mode: pre-store a VM in the fake store, then verify CreateAndWaitForPromises succeeds
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
				_, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, testNodeClaim)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		// CNI label tests are legitimately VM-only: AKS Machine handles CNI labels server-side,
		// so only VM modes need to verify they're correctly set in custom data / bootstrap.
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
				// Set kubernetes version to 1.34.0
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
				// Set kubernetes version to 1.33.0
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
			// "should use the subnet specified in the nodeclass" is now shared in
			// pkg/cloudprovider/suite_features_test.go via runSharedSubnetTests
		})

		Context("VM Creation Failures", func() {
			// "should not reattempt creation of a vm thats been created before" is now shared in
			// pkg/cloudprovider/suite_features_test.go via runSharedReuseExistingResourceTests
			It("should delete the network interface on failure to create the vm", func() {
				ErrMsg := "test error"
				ErrCode := fmt.Sprint(http.StatusNotFound)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: ErrCode,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(ErrCode, ErrMsg),
						},
					},
				)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// We should have created a nic for the vm
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				// The nic we used in the vm create, should be cleaned up if the vm call fails
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(nic.Interface.ID)
				Expect(ok).To(Equal(false))

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				pod = coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})
			// Creation failure tests (LowPriority, Overconstrained, AllocationFailed, SKUFamily quota, Regional quota)
			// are now shared in pkg/cloudprovider/suite_offerings_test.go via runSharedCreationFailureTests
		})

		Context("Ephemeral Disk", func() {
			var originalOptions *options.Options
			BeforeEach(func() {
				originalOptions = options.FromContext(ctx)
				ctx = options.ToContext(
					ctx,
					test.Options(test.OptionsFields{
						UseSIG: lo.ToPtr(true),
					}))
			})

			AfterEach(func() {
				ctx = options.ToContext(ctx, originalOptions)
			})

			Context("Placement", func() {
				It("should prefer NVMe disk if supported for ephemeral", func() {
					nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D128ds_v6"},
						},
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
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_NC24ads_A100_v4"},
						},
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
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D64s_v3"},
						},
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
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_B20ms"},
						},
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
			// 5 ephemeral disk shared tests are now in pkg/cloudprovider/suite_features_test.go via runSharedEphemeralDiskTests
			It("should select NvmeDisk for v6 skus with maxNvmeDiskSize > 0", func() {
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D128ds_v6"},
					}})
				nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](100)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				// Re-reconcile to pick up SIG images (UseSIG=true in outer context BeforeEach)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())

				Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
				Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Placement)).To(Equal(armcompute.DiffDiskPlacementNvmeDisk))
			})
		})

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

		Context("ImageReference", func() {
			// SIG image test is now shared in pkg/cloudprovider/suite_features_test.go via runSharedImageSelectionTests
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

		Context("ImageProvider + Image Family (CIG, VM-only)", func() {
			// NOTE: kubernetes version and AzureLinux image definitions must be resolved at
			// test runtime (inside the func body), NOT during ginkgo tree construction,
			// because env.KubernetesInterface is nil until BeforeEach runs.
			DescribeTable("should select the right Community Image Gallery image for a given instance type",
				func(instanceType string, imgFamily string, expectedImageDefinition string, expectedGalleryURL string) {
					// Resolve AzureLinux image definitions at runtime based on kubernetes version
					kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
					expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
					if expectUseAzureLinux3 {
						switch expectedImageDefinition {
						case imagefamily.AzureLinuxGen2ImageDefinition:
							expectedImageDefinition = imagefamily.AzureLinux3Gen2ImageDefinition
						case imagefamily.AzureLinuxGen1ImageDefinition:
							expectedImageDefinition = imagefamily.AzureLinux3Gen1ImageDefinition
						case imagefamily.AzureLinuxGen2ArmImageDefinition:
							Skip("AzureLinux3 ARM64 VHD is not available in CIG")
						}
					}

					localStatusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID)
					nodeClass.Spec.ImageFamily = lo.ToPtr(imgFamily)
					coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{instanceType},
						}})
					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, localStatusController, nodeClass)
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

					// Reset env after each nested test
					cluster.Reset()
					azureEnv.Reset()
				},
				Entry("Gen2 instance type with AKSUbuntu image family",
					"Standard_D2_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("Gen1 instance type with AKSUbuntu image family",
					"Standard_D2_v3", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("ARM instance type with AKSUbuntu image family",
					"Standard_D16plds_v5", v1beta1.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
				Entry("Gen2 instance type with AzureLinux image family",
					"Standard_D2_v5", v1beta1.AzureLinuxImageFamily, imagefamily.AzureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				Entry("Gen1 instance type with AzureLinux image family",
					"Standard_D2_v3", v1beta1.AzureLinuxImageFamily, imagefamily.AzureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
				Entry("ARM instance type with AzureLinux image family",
					"Standard_D16plds_v5", v1beta1.AzureLinuxImageFamily, imagefamily.AzureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
			)
		})

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
				kubeletFlags = expectKubeletFlagsPassed(customData)

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

		Context("LoadBalancer", func() {
			resourceGroup := "test-resourceGroup"

			It("should include loadbalancer backend pools the allocated VMs", func() {
				standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
				internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

				azureEnv.LoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
				azureEnv.LoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)

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

		Context("Basic (provisioning)", func() {
			entries := wellKnownLabelEntries()
			nonSchedulableLabels := nonSchedulableLabelsMap()

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
						expectKubeletNodeLabelsInCustomData(&vm, detail.entry.Label, detail.entry.ValueFunc())
					} else {
						expectKubeletNodeLabelsNotInCustomData(&vm, detail.entry.Label, detail.entry.ValueFunc())
					}
				}
			})

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
					expectKubeletNodeLabelsInCustomData(&vm, key, value)
				}
			})

			DescribeTable("should not write restricted labels to kubelet, but should write allowed labels", func(domain string, allowed bool) {
				nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: domain + "/team", Operator: v1.NodeSelectorOpExists}},
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: domain + "/custom-label", Operator: v1.NodeSelectorOpExists}},
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: "subdomain." + domain + "/custom-label", Operator: v1.NodeSelectorOpExists}},
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
						expectKubeletNodeLabelsInCustomData(&vm, k, v)
					} else {
						expectKubeletNodeLabelsNotInCustomData(&vm, k, v)
					}
				}
			},
				Entry("node-restriction.kubernetes.io", "node-restriction.kubernetes.io", false),
				Entry("node.kubernetes.io", "node.kubernetes.io", true),
			)
		})
	})

	// BootstrappingClient uses shared WellKnownLabelEntry and nonSchedulableLabels
	// definitions from suite_shared_provision_test.go (wellKnownLabelEntries() and nonSchedulableLabelsMap())
	Context("ProvisionMode = BootstrappingClient", func() {
		BeforeEach(func() { setupProvisionModeBootstrappingClientTestEnvironment() })
		AfterEach(func() { teardownTestEnvironment() })

		entries := wellKnownLabelEntries()
		nonSchedulableLabels := nonSchedulableLabelsMap()
		_ = nonSchedulableLabels // used in test closures below

		It("should provision the node and CSE", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectCSEProvisioned(azureEnv)
			ExpectScheduled(ctx, env.Client, pod)
		})
		It("should not reattempt creation of a vm thats been created before, and also not CSE", func() {
			// This test is more like a sanity check of the current intended behavior. The design of the behavior can be changed if intended.
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
			_, err := cloudProvider.Create(ctx, testNodeClaim) // Async routine can still be ran in the background after this point
			Expect(err).ToNot(HaveOccurred())

			ExpectCSENotProvisioned(azureEnv)
		})

		DescribeTable(
			"should support individual instance type labels (when all pods scheduled individually)",
			func(item WellKnownLabelEntry) {
				if item.SetupFunc != nil {
					item.SetupFunc()
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				// Re-reconcile status after applying, in case SetupFunc changed image family or FIPS mode
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				value := item.ValueFunc()

				pod := coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{item.Label: value}})
				// Simulate multiple scheduling passes before final binding, this ensures that when real scheduling happens we won't
				// end up with a new node for each scheduling attempt
				if item.Label != v1.LabelWindowsBuild { // TODO: special case right now as we don't support it
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

				if item.ExpectedOnNode {
					Expect(node.Labels[item.Label]).To(Equal(value))
				} else {
					Expect(node.Labels).ToNot(HaveKey(item.Label))
				}

				// Get the bootstrap API input
				Expect(azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
				bootstrapInput := azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
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

		It("should write other (non-schedulable) labels to kubelet on bootstrap API", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Not checking on the node as not all these labels are expected there (via Karpenter setting them, they'll get there via kubelet)

			Expect(azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Len()).To(Equal(1))
			bootstrapInput := azureEnv.NodeBootstrappingAPI.NodeBootstrappingGetBehavior.CalledWithInput.Pop()
			for key, value := range nonSchedulableLabels {
				Expect(bootstrapInput.Params.ProvisionProfile.CustomNodeLabels).To(HaveKeyWithValue(key, value))
			}
		})
	})
})

func createSDKErrorBody(code, message string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf(`{"error":{"code": "%s", "message": "%s"}}`, code, message))))
}

func expectKubeletFlagsPassed(customData string) string {
	GinkgoHelper()
	return customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
}

func expectKubeletNodeLabelsPassed(customData string) string {
	GinkgoHelper()
	startIdx := strings.Index(customData, "KUBELET_NODE_LABELS=") + len("KUBELET_NODE_LABELS=")
	endIdx := strings.Index(customData[startIdx:], "\n")
	if endIdx == -1 {
		// If no newline found, take to the end
		return customData[startIdx:]
	}
	return customData[startIdx : startIdx+endIdx]
}

func expectKubeletNodeLabelsInCustomData(vm *armcompute.VirtualMachine, key string, value string) {
	GinkgoHelper()

	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.OSProfile).ToNot(BeNil())
	Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	// Extract and check KUBELET_NODE_LABELS contains the expected label
	kubeletNodeLabels := expectKubeletNodeLabelsPassed(decodedString)
	Expect(kubeletNodeLabels).To(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
}

func expectKubeletNodeLabelsNotInCustomData(vm *armcompute.VirtualMachine, key string, value string) {
	GinkgoHelper()

	Expect(vm.Properties).ToNot(BeNil())
	Expect(vm.Properties.OSProfile).ToNot(BeNil())
	Expect(vm.Properties.OSProfile.CustomData).ToNot(BeNil())

	customData := *vm.Properties.OSProfile.CustomData
	Expect(customData).ToNot(BeNil())

	decodedBytes, err := base64.StdEncoding.DecodeString(customData)
	Expect(err).To(Succeed())
	decodedString := string(decodedBytes[:])

	// Extract and check KUBELET_NODE_LABELS contains the expected label
	kubeletNodeLabels := expectKubeletNodeLabelsPassed(decodedString)
	Expect(kubeletNodeLabels).ToNot(ContainSubstring(fmt.Sprintf("%s=%s", key, value)))
}
