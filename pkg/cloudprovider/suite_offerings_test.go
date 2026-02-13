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

	"github.com/awslabs/operatorpkg/object"
	corestatus "github.com/awslabs/operatorpkg/status"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/Azure/skewer"
)

var _ = Describe("CloudProvider - Offerings", func() {

	// === SHARED TEST FUNCTIONS ===
	// These run for both AKSMachineAPI and AKSScriptless (VM) modes.

	runSharedCreationFailureTests := func(mode provisionTestMode) {
		Context("Create - Expected Creation Failures", func() {
			It("should fail to provision when LowPriorityCoresQuota errors are hit, then switch capacity type and succeed", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      karpv1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				mode.setError(errLowPriorityQuota)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(BeNumerically(">=", 1))
				result := mode.popCreationResult()
				testSKU := &skewer.SKU{Name: lo.ToPtr(result.vmSize)}
				Expect(result.zoneErr).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, testSKU, result.zone, karpv1.CapacityTypeSpot)

				mode.clearError()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))

				nodes, err := env.KubernetesInterface.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(nodes.Items)).To(Equal(1))
				Expect(nodes.Items[0].Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			It("should fail to provision when OverconstrainedZonalAllocation errors are hit, then switch zone and succeed", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      karpv1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				mode.setError(errOverconstrainedZonal)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(BeNumerically(">=", 1))
				result := mode.popCreationResult()
				Expect(result.zoneErr).ToNot(HaveOccurred())
				testSKU := &skewer.SKU{Name: lo.ToPtr(result.vmSize)}
				ExpectUnavailable(azureEnv, testSKU, result.zone, karpv1.CapacityTypeSpot)

				mode.clearError()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(result.zone))
			})

			It("should fail to provision when OverconstrainedAllocation errors are hit, then switch capacity type and succeed", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      karpv1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				mode.setError(errOverconstrained)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(BeNumerically(">=", 1))
				result := mode.popCreationResult()
				testSKU := &skewer.SKU{Name: lo.ToPtr(result.vmSize)}
				Expect(result.zoneErr).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, testSKU, result.zone, karpv1.CapacityTypeSpot)

				mode.clearError()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			It("should fail to provision when AllocationFailure errors are hit, then switch VM size and succeed", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v3", "Standard_D64s_v3"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				mode.setError(errAllocationFailed)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				Expect(mode.getCreateCallCount()).To(BeNumerically(">=", 1))
				result := mode.popCreationResult()
				Expect(result.zoneErr).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, &skewer.SKU{Name: lo.ToPtr(result.vmSize)}, result.zone, karpv1.CapacityTypeSpot)

				mode.clearError()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).ToNot(Equal(result.vmSize))
			})

			It("should fail to provision when VM SKU family vCPU quota exceeded error is returned, and succeed when it is gone", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				mode.setError(errSKUFamilyQuota)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(mode.getCreateCallCount()).To(BeNumerically(">=", 1))

				// VM mode: verify NIC cleanup after creation failure (preserved from original VM test)
				if mode.isVM {
					Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
					Expect(nic).NotTo(BeNil())
					_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(nic.Interface.ID)
					Expect(ok).To(Equal(false))
				}

				mode.clearError()
				pod = coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})

			It("should fail to provision when VM SKU family vCPU quota limit is zero, and succeed when its gone", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				mode.setError(errSKUFamilyQuotaZero)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(mode.getCreateCallCount()).To(BeNumerically(">=", 1))

				// VM mode: verify NIC cleanup after creation failure (preserved from original VM test)
				if mode.isVM {
					Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
					nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
					Expect(nic).NotTo(BeNil())
					_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(nic.Interface.ID)
					Expect(ok).To(Equal(false))
				}

				mode.clearError()
				pod = coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})

			It("should return ICE if Total Regional Cores Quota errors are hit", func() {
				mode.setError(errRegionalCoresQuota)

				testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass, testNodeClaim)
				claim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, testNodeClaim)
				Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
				Expect(claim).To(BeNil())
			})
		})
	}

	runSharedZoneAwareTests := func(mode provisionTestMode) {
		Context("Create - Zone-aware provisioning", func() {
			It("should launch in the NodePool-requested zone", func() {
				zone, rawZone := fmt.Sprintf("%s-3", fake.Region), "3"
				nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeSpot, karpv1.CapacityTypeOnDemand}}},
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{zone}}},
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, zone))

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.zones).To(ConsistOf(&rawZone))
			})

			It("should support provisioning in non-zonal regions", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, azureEnvNonZonal, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Mode-specific creation check: verify the correct API was called (preserved from originals)
				if mode.isVM {
					Expect(azureEnvNonZonal.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				} else {
					Expect(azureEnvNonZonal.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				}

				// Verify that zones are empty for non-zonal regions
				if mode.isVM {
					vm := azureEnvNonZonal.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
					Expect(vm.Zones).To(BeEmpty())
				} else {
					m := azureEnvNonZonal.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
					Expect(m.Zones).To(BeEmpty())
				}
			})

			It("should support provisioning non-zonal instance types in zonal regions", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_NC6s_v3"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, ""))

				Expect(mode.getCreateCallCount()).To(Equal(1))
				result := mode.popCreationResult()
				Expect(result.zones).To(BeEmpty())
			})
		})
	}

	runSharedErrorCaseTests := func(mode provisionTestMode) {
		Context("Create - CloudProvider Create Error Cases", func() {
			It("should return an ICE error when there are no instance types to launch", func() {
				nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"doesnotexist"},
						},
					},
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
				cloudProviderMachine, err := CreateAndDrain(ctx, cloudProvider, azureEnv, nodeClaim)
				Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
				Expect(cloudProviderMachine).To(BeNil())
			})

			It("should return error when NodeClass readiness is Unknown", func() {
				nodeClass.StatusConditions().SetUnknown(corestatus.ConditionReady)
				testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass, testNodeClaim)
				claim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, testNodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("resolving NodeClass readiness, NodeClass is in Ready=Unknown"))
			})

			It("should return error when instance type resolution fails", func() {
				localStatusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI)
				nodeClass.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, localStatusController, nodeClass)

				azureEnv.SKUsAPI.Error = fmt.Errorf("failed to list SKUs")
				testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})
				claim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, testNodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("resolving instance types"))
				Expect(err.Error()).To(ContainSubstring("failed to list SKUs"))
				azureEnv.SKUsAPI.Error = nil
			})

			It("should return error when instance creation fails", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})
				mode.setError(errGenericCreation)
				claim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, testNodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(SatisfyAny(
					ContainSubstring("creating instance failed"),
					ContainSubstring("creating AKS machine failed"),
				))
			})
		})
	}

	runSharedUnavailableOfferingsTests := func(mode provisionTestMode) {
		Context("Create - Unavailable Offerings", func() {
			It("should not allocate in a zone marked as unavailable", func() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeSpot)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeOnDemand)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v2"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(fakeZone1))
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
			})

			It("should handle ZonalAllocationFailed on creating the instance", func() {
				mode.setZoneAllocError("Standard_D2_v2", "1")

				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v2"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				if mode.isVM {
					ExpectLaunched(ctx, env.Client, cloudProvider, coreProvisioner, pod)
				} else {
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				}
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the failed NodeClaim is cleaned up (VM mode only - AKS Machine mode doesn't clean up synchronously)
				if mode.isVM {
					Eventually(func() []*karpv1.NodeClaim { return ExpectNodeClaims(ctx, env.Client) }).To(HaveLen(0))
				}

				By("marking whatever zone was picked as unavailable - for both spot and on-demand")
				expectedUnavailableSKUs := []*skewer.SKU{
					{Name: lo.ToPtr("Standard_D2_v2"), Size: lo.ToPtr("D2_v2"), Family: lo.ToPtr("StandardDv2Family"),
						Capabilities: &[]compute.ResourceSkuCapabilities{{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("2")}}},
					{Name: lo.ToPtr("Standard_D16_v2"), Size: lo.ToPtr("D16_v2"), Family: lo.ToPtr("StandardDv2Family"),
						Capabilities: &[]compute.ResourceSkuCapabilities{{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("16")}}},
					{Name: lo.ToPtr("Standard_D32_v2"), Size: lo.ToPtr("D32_v2"), Family: lo.ToPtr("StandardDv2Family"),
						Capabilities: &[]compute.ResourceSkuCapabilities{{Name: lo.ToPtr("vCPUs"), Value: lo.ToPtr("32")}}},
				}

				Expect(mode.getCreateCallCount()).To(BeNumerically(">", 0))
				result := mode.popCreationResult()
				Expect(result.zoneErr).ToNot(HaveOccurred())

				for _, skuToCheck := range expectedUnavailableSKUs {
					Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable(skuToCheck, result.zone, karpv1.CapacityTypeSpot)).To(BeTrue())
					Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable(skuToCheck, result.zone, karpv1.CapacityTypeOnDemand)).To(BeTrue())
				}

				By("successfully scheduling in a different zone on retry")
				// VM mode: original test did NOT clear the error before retry (async Error is consumed)
				// AKS Machine mode: must clear the provisioning error override
				if !mode.isVM {
					mode.clearError()
				}
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(result.zone))
				// AKS Machine mode: verify retry actually made a creation call (preserved from original)
				if !mode.isVM {
					Expect(mode.getCreateCallCount()).To(BeNumerically(">", 0))
				}
			})

			Context("should not return unavailable offerings", func() {
				It("zonal", func() {
					for _, zone := range azureEnv.Zones() {
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}
					instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())
					seeUnavailable := false
					for _, instanceType := range instanceTypes {
						if instanceType.Name == "Standard_D2_v2" {
							seeUnavailable = true
							Expect(len(instanceType.Offerings.Available())).To(Equal(0))
						} else {
							Expect(len(instanceType.Offerings.Available())).To(Not(Equal(0)))
						}
					}
					Expect(seeUnavailable).To(BeTrue())
				})
				It("non-zonal", func() {
					for _, zone := range azureEnvNonZonal.Zones() {
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}
					instanceTypes, err := azureEnvNonZonal.InstanceTypesProvider.List(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())
					seeUnavailable := false
					for _, instanceType := range instanceTypes {
						if instanceType.Name == "Standard_D2_v2" {
							seeUnavailable = true
							Expect(len(instanceType.Offerings.Available())).To(Equal(0))
						} else {
							Expect(len(instanceType.Offerings.Available())).To(Not(Equal(0)))
						}
					}
					Expect(seeUnavailable).To(BeTrue())
				})
			})

			It("should launch instances in a different zone than preferred when zone is unavailable", func() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeOnDemand)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeSpot)

				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
				})
				pod.Spec.Affinity = &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
							{
								Weight: 1,
								Preference: v1.NodeSelectorTerm{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{fakeZone1}},
									},
								},
							},
						},
					},
				}
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(fakeZone1))
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
			})

			It("should launch smaller instances than optimal if larger instance launch results in Insufficient Capacity Error", func() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fakeZone1, karpv1.CapacityTypeOnDemand)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fakeZone1, karpv1.CapacityTypeSpot)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_DS2_v2", "Standard_F16s_v2"},
					},
				})
				pods := []*v1.Pod{}
				for i := 0; i < 2; i++ {
					pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{
						ResourceRequirements: v1.ResourceRequirements{
							Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1")},
						},
						NodeSelector: map[string]string{v1.LabelTopologyZone: fakeZone1},
					}))
				}
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pods...)
				nodeNames := sets.New[string]()
				for _, pod := range pods {
					node := ExpectScheduled(ctx, env.Client, pod)
					Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_DS2_v2"))
					nodeNames.Insert(node.Name)
				}
				Expect(nodeNames.Len()).To(Equal(2))
			})

			Context("should launch instances on later reconciliation attempt with ICE Cache expiry", func() {
				It("zonal", func() {
					for _, zone := range azureEnv.Zones() {
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod(coretest.PodOptions{
						NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
					})
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectNotScheduled(ctx, env.Client, pod)
					azureEnv.UnavailableOfferingsCache.Flush()
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					node := ExpectScheduled(ctx, env.Client, pod)
					Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_D2_v2"))
				})
				It("non-zonal", func() {
					for _, zone := range azureEnvNonZonal.Zones() {
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod(coretest.PodOptions{
						NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
					})
					ExpectProvisionedAndDrained(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, azureEnvNonZonal, pod)
					ExpectNotScheduled(ctx, env.Client, pod)
					azureEnvNonZonal.UnavailableOfferingsCache.Flush()
					ExpectProvisionedAndDrained(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, azureEnvNonZonal, pod)
					node := ExpectScheduled(ctx, env.Client, pod)
					Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_D2_v2"))
				})
			})

			Context("SKUNotAvailable", func() {
				AssertUnavailable := func(sku *skewer.SKU, capacityType string) {
					mode.setSkuNotAvailable(sku.GetName())
					coretest.ReplaceRequirements(nodePool,
						karpv1.NodeSelectorRequirementWithMinValues{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku.GetName()}}},
						karpv1.NodeSelectorRequirementWithMinValues{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{capacityType}}},
					)
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectNotScheduled(ctx, env.Client, pod)
					for _, zoneID := range []string{"1", "2", "3"} {
						ExpectUnavailable(azureEnv, sku, utils.MakeAKSLabelZoneFromARMZone(fake.Region, zoneID), capacityType)
					}
				}

				It("should mark SKU as unavailable in all zones for Spot", func() {
					AssertUnavailable(defaultTestSKU, karpv1.CapacityTypeSpot)
				})
				It("should mark SKU as unavailable in all zones for OnDemand", func() {
					AssertUnavailable(defaultTestSKU, karpv1.CapacityTypeOnDemand)
				})
			})
		})
	}

	// === MODE CONTEXTS ===

	Context("ProvisionMode = AKSMachineAPI", func() {
		BeforeEach(func() { setupAKSMachineAPIMode() })
		AfterEach(func() { teardownProvisionMode() })

		mode := aksMachineProvisionMode()
		runSharedCreationFailureTests(mode)
		runSharedZoneAwareTests(mode)
		runSharedErrorCaseTests(mode)
		runSharedUnavailableOfferingsTests(mode)
	})

	Context("ProvisionMode = AKSScriptless", func() {
		BeforeEach(func() { setupVMMode() })
		AfterEach(func() { teardownProvisionMode() })

		mode := vmProvisionMode()
		runSharedCreationFailureTests(mode)
		runSharedZoneAwareTests(mode)
		runSharedErrorCaseTests(mode)
		runSharedUnavailableOfferingsTests(mode)
	})
})
