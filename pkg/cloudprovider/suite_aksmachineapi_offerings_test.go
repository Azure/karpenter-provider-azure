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
	"k8s.io/client-go/tools/record"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/Azure/skewer"
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
			cluster.Reset()
			azureEnv.Reset()
			azureEnvNonZonal.Reset()
		})

		Context("Create - Expected Creation Failures", func() {
			// Ported from VM test: "should fail to provision when LowPriorityCoresQuota errors are hit, then switch capacity type and succeed"
			It("should fail to provision when LowPriorityCoresQuota errors are hit, then switch capacity type and succeed", func() {
				// Configure NodePool to allow both spot and on-demand
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      karpv1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Set up async error
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorLowPriorityCoresQuota(fake.Region)

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

				// Verify spot capacity type marked as unavailable due to quota error
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				testSKU := &skewer.SKU{Name: lo.ToPtr(vmSize)}
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, testSKU, zone, karpv1.CapacityTypeSpot)

				// Clear both error and output for retry - should succeed with on-demand
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))

				// Verify final node count
				nodes, err := env.KubernetesInterface.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(nodes.Items)).To(Equal(1))
				Expect(nodes.Items[0].Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			// Ported from VM test: "should fail to provision when OverconstrainedZonalAllocation errors are hit, then switch zone and succeed"
			It("should fail to provision when OverconstrainedZonalAllocation errors are hit, then switch zone and succeed", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      karpv1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
					}})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorOverconstrainedZonalAllocation()

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed due to zonal allocation constraint
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				initialZone, err := instance.GetAKSLabelZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())

				// Verify initial zone marked as unavailable due to zonal allocation failure
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				testSKU := &skewer.SKU{Name: lo.ToPtr(vmSize)}
				ExpectUnavailable(azureEnv, testSKU, initialZone, karpv1.CapacityTypeSpot)

				// Clear the error and retry - should succeed with different zone
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(initialZone))
			})

			// Ported from VM test: "should fail to provision when OverconstrainedAllocation errors are hit, then switch capacity type and succeed"
			It("should fail to provision when OverconstrainedAllocation errors are hit, then switch capacity type and succeed", func() {
				// Configure NodePool to allow multiple capacity types
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      karpv1.CapacityTypeLabelKey,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorOverconstrainedAllocation()

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed due to overconstrained allocation
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

				// Verify spot capacity type marked as unavailable due to allocation error
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				testSKU := &skewer.SKU{Name: lo.ToPtr(vmSize)}
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, testSKU, zone, karpv1.CapacityTypeSpot)

				// Clear both error and output for retry - should succeed with on-demand
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
			})

			// Ported from VM test: "should fail to provision when AllocationFailure errors are hit, then switch VM size and succeed"
			It("should fail to provision when AllocationFailure errors are hit, then switch VM size and succeed", func() {
				// Configure NodePool to allow multiple instance types
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v3", "Standard_D64s_v3"},
					},
				})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAllocationFailed()

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed due to allocation failure
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				initialVMSize := lo.FromPtr(aksMachine.Properties.Hardware.VMSize)

				// Verify initial VM size marked as unavailable due to allocation failure
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&aksMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, &skewer.SKU{Name: lo.ToPtr(initialVMSize)}, zone, karpv1.CapacityTypeSpot)

				// Clear the error and retry - should succeed with different VM size
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).ToNot(Equal(initialVMSize))
			})

			// Ported from VM test: "should fail to provision when VM SKU family vCPU quota exceeded error is returned, and succeed when it is gone"
			It("should fail to provision when VM SKU family vCPU quota exceeded error is returned, and succeed when it is gone", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded("westus2", "Standard NCASv3_T4", 24, 24, 8, 32)

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed due to family quota
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

				// Clear the error and retry - should succeed
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})

			// Ported from VM test: "should fail to provision when VM SKU family vCPU quota limit is zero, and succeed when its gone"
			It("should fail to provision when VM SKU family vCPU quota limit is zero, and succeed when its gone", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded("westus2", "Standard NCASv3_T4", 0, 0, 8, 8)

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed due to zero quota limit
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

				// Clear the error and retry - should succeed
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})

			// Ported from VM test: Total Regional Cores quota test pattern
			It("should return ICE if Total Regional Cores Quota errors are hit", func() {
				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorTotalRegionalCoresQuota(fake.Region)

				// Create nodeClaim directly and call cloudProvider.Create like VM tests
				testNodeClaim1 := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							karpv1.NodePoolLabelKey: nodePool.Name,
						},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, testNodeClaim1)
				claim, err := cloudProvider.Create(ctx, testNodeClaim1)
				Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
				Expect(claim).To(BeNil())
			})
		})

		// Ported from VM test: "Zone-aware provisioning"
		Context("Create - Zone-aware provisioning", func() {
			// Ported from VM test: "should launch in the NodePool-requested zone"
			It("should launch in the NodePool-requested zone", func() {
				zone, aksMachineZone := fmt.Sprintf("%s-3", fake.Region), "3"
				nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeSpot, karpv1.CapacityTypeOnDemand}}},
					{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{zone}}},
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, zone))

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine).NotTo(BeNil())
				Expect(aksMachine.Zones).To(ConsistOf(&aksMachineZone))
			})

			// Ported from VM test: "should support provisioning in non-zonal regions"
			It("should support provisioning in non-zonal regions", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnvNonZonal.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnvNonZonal.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Zones).To(BeEmpty())
			})

			// Ported from VM test: "should support provisioning non-zonal instance types in zonal regions"
			It("should support provisioning non-zonal instance types in zonal regions", func() {
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_NC6s_v3"}, // Non-zonal instance type
					}})
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)

				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, ""))

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Zones).To(BeEmpty())
			})
		})

		// Ported from VM test: "CloudProvider Create Error Cases"
		Context("Create - CloudProvider Create Error Cases", func() {
			// Ported from VM test: "should return an ICE error when there are no instance types to launch"
			// But, from cloudprovider/suite_test.go rather than instancetype/suite_test.go
			It("should return an ICE error when there are no instance types to launch", func() {
				// Specify no instance types and expect to receive a capacity error
				nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"doesnotexist"}, // will not match any instance types
						},
					},
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
				cloudProviderMachine, err := cloudProvider.Create(ctx, nodeClaim)
				Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
				Expect(cloudProviderMachine).To(BeNil())
			})

			// Ported from VM test: "should return error when NodeClass readiness is Unknown"
			It("should return error when NodeClass readiness is Unknown", func() {
				nodeClass.StatusConditions().SetUnknown(corestatus.ConditionReady)
				testNodeClaim2 := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							karpv1.NodePoolLabelKey: nodePool.Name,
						},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, testNodeClaim2)
				claim, err := cloudProvider.Create(ctx, testNodeClaim2)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("resolving NodeClass readiness, NodeClass is in Ready=Unknown"))
			})

			// Ported from VM test: "should return error when instance type resolution fails"
			It("should return error when instance type resolution fails", func() {
				// Create and set up the status controller
				localStatusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI)

				// Set NodeClass to Ready
				nodeClass.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Reconcile the NodeClass to ensure status is updated
				ExpectObjectReconciled(ctx, env.Client, localStatusController, nodeClass)

				azureEnv.SKUsAPI.Error = fmt.Errorf("failed to list SKUs")

				testNodeClaim3 := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							karpv1.NodePoolLabelKey: nodePool.Name,
						},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})

				claim, err := cloudProvider.Create(ctx, testNodeClaim3)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("failed to list SKUs"))

				// Clean up the error for other tests
				azureEnv.SKUsAPI.Error = nil
			})

			// Ported from VM test: "should return error when instance creation fails"
			It("should return error when instance creation fails", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Create a NodeClaim with valid requirements
				testNodeClaim4 := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							karpv1.NodePoolLabelKey: nodePool.Name,
						},
					},
					Spec: karpv1.NodeClaimSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name:  nodeClass.Name,
							Group: object.GVK(nodeClass).Group,
							Kind:  object.GVK(nodeClass).Kind,
						},
					},
				})

				// Set up the AKS machine provider to fail (different from VM API)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAny()

				claim, err := cloudProvider.Create(ctx, testNodeClaim4)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("creating AKS machine failed"))
			})
		})

		// Mostly ported from VM test: "Provider list"
		Context("Create - Provider list", func() {
			// Ported from VM test: "should support individual instance type labels"
			// TODO(mattchr): rework this from VM test (new additions)
			// It("should support individual instance type labels", func() {
			// 	ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			// 	nodeSelector := map[string]string{
			// 		// Well known
			// 		v1.LabelTopologyRegion:      fake.Region,
			// 		karpv1.NodePoolLabelKey:     nodePool.Name,
			// 		v1.LabelTopologyZone:        fakeZone1,
			// 		v1.LabelInstanceTypeStable:  "Standard_NC24ads_A100_v4",
			// 		v1.LabelOSStable:            "linux",
			// 		v1.LabelArchStable:          "amd64",
			// 		karpv1.CapacityTypeLabelKey: "on-demand",
			// 		// Well Known to AKS
			// 		v1beta1.LabelSKUName:                      "Standard_NC24ads_A100_v4",
			// 		v1beta1.LabelSKUFamily:                    "N",
			// 		v1beta1.LabelSKUVersion:                   "4",
			// 		v1beta1.LabelSKUStorageEphemeralOSMaxSize: "429",
			// 		v1beta1.LabelSKUAcceleratedNetworking:     "true",
			// 		v1beta1.LabelSKUStoragePremiumCapable:     "true",
			// 		v1beta1.LabelSKUGPUName:                   "A100",
			// 		v1beta1.LabelSKUGPUManufacturer:           "nvidia",
			// 		v1beta1.LabelSKUGPUCount:                  "1",
			// 		v1beta1.LabelSKUCPU:                       "24",
			// 		v1beta1.LabelSKUMemory:                    "8192",
			// 		// Deprecated Labels
			// 		v1.LabelFailureDomainBetaRegion:    fake.Region,
			// 		v1.LabelFailureDomainBetaZone:      fakeZone1,
			// 		"beta.kubernetes.io/arch":          "amd64",
			// 		"beta.kubernetes.io/os":            "linux",
			// 		v1.LabelInstanceType:               "Standard_NC24ads_A100_v4",
			// 		"topology.disk.csi.azure.com/zone": fakeZone1,
			// 		v1.LabelWindowsBuild:               "window",
			// 		// Cluster Label
			// 		v1beta1.AKSLabelCluster: "test-cluster",
			// 	}

			// // Ensure that we're exercising all well known labels
			// Expect(lo.Keys(nodeSelector)).To(ContainElements(append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)))

			// var pods []*v1.Pod
			// 	for key, value := range nodeSelector {
			// 		pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{key: value}}))
			// 	}
			// 	ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pods...)
			// 	for _, pod := range pods {
			// 		ExpectScheduled(ctx, env.Client, pod)
			// 	}
			// })
		})

		// Ported from VM test: "Unavailable Offerings"
		Context("Create - Unavailable Offerings", func() {
			// Ported from VM test: "should not allocate a vm in a zone marked as unavailable"
			It("should not allocate an AKS machine in a zone marked as unavailable", func() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeSpot)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeOnDemand)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v2"},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(fakeZone1))
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
			})

			// Ported from VM test: "should handle ZonalAllocationFailed on creating the VM"
			It("should handle ZonalAllocationFailed on creating the AKS machine", func() {
				// Set up async error via BOTH Error and Output (LRO returns both)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorZoneAllocationFailed("Standard_D2_v2", "1")

				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v2"},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				By("marking whatever zone was picked as unavailable - for both spot and on-demand")
				// When ZonalAllocationFailed error is encountered, we block all VM sizes that have >= vCPUs as the VM size for which we encountered the error
				expectedUnavailableSKUs := []*skewer.SKU{
					{
						Name:   lo.ToPtr("Standard_D2_v2"),
						Size:   lo.ToPtr("D2_v2"),
						Family: lo.ToPtr("StandardDv2Family"),
						Capabilities: &[]compute.ResourceSkuCapabilities{
							{
								Name:  lo.ToPtr("vCPUs"),
								Value: lo.ToPtr("2"),
							},
						},
					},
					{
						Name:   lo.ToPtr("Standard_D16_v2"),
						Size:   lo.ToPtr("D16_v2"),
						Family: lo.ToPtr("StandardDv2Family"),
						Capabilities: &[]compute.ResourceSkuCapabilities{
							{
								Name:  lo.ToPtr("vCPUs"),
								Value: lo.ToPtr("16"),
							},
						},
					},
					{
						Name:   lo.ToPtr("Standard_D32_v2"),
						Size:   lo.ToPtr("D32_v2"),
						Family: lo.ToPtr("StandardDv2Family"),
						Capabilities: &[]compute.ResourceSkuCapabilities{
							{
								Name:  lo.ToPtr("vCPUs"),
								Value: lo.ToPtr("32"),
							},
						},
					},
				}

				// For AKS Machine API, we need to determine the zone from the machine creation attempt
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">", 0))
				machineInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()

				// Extract zone from AKS machine - similar to VM test pattern
				failedZone, err := instance.GetAKSLabelZoneFromAKSMachine(&machineInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())

				for _, skuToCheck := range expectedUnavailableSKUs {
					Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable(skuToCheck, failedZone, karpv1.CapacityTypeSpot)).To(BeTrue())
					Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable(skuToCheck, failedZone, karpv1.CapacityTypeOnDemand)).To(BeTrue())
				}

				By("successfully scheduling in a different zone on retry")
				// Clear the error and verify retry succeeds in different zone
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil

				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				// Verify machine was created in a different zone than the failed one
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(failedZone))
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">", 0))
			})

			// Ported from VM test: DescribeTable "Should not return unavailable offerings"
			Context("should not return unavailable offerings", func() {
				It("should not return unavailable offerings - zonal", func() {
					for _, zone := range azureEnv.Zones() {
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}
					instanceTypes, err := azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					seeUnavailable := false
					for _, instanceType := range instanceTypes {
						if instanceType.Name == "Standard_D2_v2" {
							// We want to validate we see the offering in the list,
							// but we also expect it to not have any available offerings
							seeUnavailable = true
							Expect(len(instanceType.Offerings.Available())).To(Equal(0))
						} else {
							Expect(len(instanceType.Offerings.Available())).To(Not(Equal(0)))
						}
					}
					// we should see the unavailable offering in the list
					Expect(seeUnavailable).To(BeTrue())
				})
				It("should not return unavailable offerings - non-zonal", func() {
					for _, zone := range azureEnvNonZonal.Zones() {
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}
					instanceTypes, err := azureEnvNonZonal.InstanceTypesProvider.List(ctx, nodeClass)
					Expect(err).ToNot(HaveOccurred())

					seeUnavailable := false
					for _, instanceType := range instanceTypes {
						if instanceType.Name == "Standard_D2_v2" {
							// We want to validate we see the offering in the list,
							// but we also expect it to not have any available offerings
							seeUnavailable = true
							Expect(len(instanceType.Offerings.Available())).To(Equal(0))
						} else {
							Expect(len(instanceType.Offerings.Available())).To(Not(Equal(0)))
						}
					}
					// we should see the unavailable offering in the list
					Expect(seeUnavailable).To(BeTrue())
				})
			})

			// Ported from VM test: "should launch instances in a different zone than preferred"
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
										{
											Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{fakeZone1},
										},
									},
								},
							},
						},
					},
				}
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(fakeZone1))
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
			})

			// Ported from VM test: "should launch smaller instances than optimal if larger instance launch results in Insufficient Capacity Error"
			It("should launch smaller instances than optimal if larger instance launch results in Insufficient Capacity Error", func() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fakeZone1, karpv1.CapacityTypeOnDemand)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fakeZone1, karpv1.CapacityTypeSpot)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_DS2_v2", "Standard_F16s_v2"},
					}})
				pods := []*v1.Pod{}
				for i := 0; i < 2; i++ {
					pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{
						ResourceRequirements: v1.ResourceRequirements{
							Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1")},
						},
						NodeSelector: map[string]string{
							v1.LabelTopologyZone: fakeZone1,
						},
					}))
				}
				// Provisions 2 smaller instances since larger was ICE'd
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pods...)

				nodeNames := sets.New[string]()
				for _, pod := range pods {
					node := ExpectScheduled(ctx, env.Client, pod)
					Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_DS2_v2"))
					nodeNames.Insert(node.Name)
				}
				Expect(nodeNames.Len()).To(Equal(2))
			})

			// Ported from VM test: "should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry"
			Context("should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry", func() {
				It("should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry - zonal", func() {
					for _, zone := range azureEnv.Zones() {
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}

					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod(coretest.PodOptions{
						NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
					})
					ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
					ExpectNotScheduled(ctx, env.Client, pod)

					// capacity shortage is over - expire the items from the cache and try again
					azureEnv.UnavailableOfferingsCache.Flush()
					ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
					node := ExpectScheduled(ctx, env.Client, pod)
					Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_D2_v2"))
				})
				It("should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry - non-zonal", func() {
					for _, zone := range azureEnvNonZonal.Zones() {
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
						azureEnvNonZonal.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
					}

					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod(coretest.PodOptions{
						NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
					})
					ExpectProvisioned(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, pod)
					ExpectNotScheduled(ctx, env.Client, pod)

					// capacity shortage is over - expire the items from the cache and try again
					azureEnvNonZonal.UnavailableOfferingsCache.Flush()
					ExpectProvisioned(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, pod)
					node := ExpectScheduled(ctx, env.Client, pod)
					Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_D2_v2"))
				})
			})

			// Ported from VM test context: "SkuNotAvailable"
			Context("SKUNotAvailable", func() {
				AssertUnavailable := func(sku *skewer.SKU, capacityType string) {
					// Simulate SKU not available error via AKS Machine API
					azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorSkuNotAvailable(sku.GetName(), fake.Region)

					coretest.ReplaceRequirements(nodePool,
						karpv1.NodeSelectorRequirementWithMinValues{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku.GetName()}}},
						karpv1.NodeSelectorRequirementWithMinValues{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{capacityType}}},
					)
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod()
					ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
					ExpectNotScheduled(ctx, env.Client, pod)
					for _, zoneID := range []string{"1", "2", "3"} {
						ExpectUnavailable(azureEnv, sku, utils.MakeAKSLabelZoneFromARMZone(fake.Region, zoneID), capacityType)
					}
				}

				// Ported from VM test: "should mark SKU as unavailable in all zones for Spot"
				It("should mark SKU as unavailable in all zones for Spot", func() {
					AssertUnavailable(defaultTestSKU, karpv1.CapacityTypeSpot)
				})

				// Ported from VM test: "should mark SKU as unavailable in all zones for OnDemand"
				It("should mark SKU as unavailable in all zones for OnDemand", func() {
					AssertUnavailable(defaultTestSKU, karpv1.CapacityTypeOnDemand)
				})
			})
		})

	})
})
