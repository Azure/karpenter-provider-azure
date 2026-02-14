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

// TODO v1beta1 extra refactor into suite_test.go / cloudprovider_test.go
import (
	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

func validateAKSMachineNodeClaim(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Common validations
	validateNodeClaimCommon(nodeClaim, nodePool)

	// AKS-specific annotations
	Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
	Expect(nodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]).ToNot(BeEmpty())
}

// runSharedAKSMachineAPITests contains the common test cases that should be run
// for both ManageExistingAKSMachines = true and false configurations
func runSharedAKSMachineAPITests() {
	It("should be able to handle basic operations", func() {
		ExpectApplied(ctx, env.Client, nodeClass, nodePool)

		// List should return nothing
		azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
		azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
		nodeClaims, err := cloudProvider.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClaims).To(BeEmpty())
		Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1)) // Expect to be called in case of existing VMs
		Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolGetBehavior.CalledWithInput.Len()).To(Equal(0))                     // No unnecessary checks

		// Scale-up 1 node
		azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
		pod := coretest.UnschedulablePod()
		ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		//// Should call AKS Machine APIs instead of VM APIs
		Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
		Expect(createInput.AKSMachine.Properties).ToNot(BeNil())

		// List should return the created nodeclaim
		azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
		azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
		nodeClaims, err = cloudProvider.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1)) // Expect to be called in case of existing VMs

		//// The returned nodeClaim should be correct
		Expect(nodeClaims).To(HaveLen(1))
		createdNodeClaim := nodeClaims[0]
		validateAKSMachineNodeClaim(createdNodeClaim, nodePool)

		// Get should return the created nodeClaim
		azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
		azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
		retrievedNodeClaim, err := cloudProvider.Get(ctx, createdNodeClaim.Status.ProviderID)
		Expect(err).ToNot(HaveOccurred())
		Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

		//// The returned nodeClaim should be correct
		validateAKSMachineNodeClaim(retrievedNodeClaim, nodePool)

		// Delete
		azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
		azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
		Expect(cloudProvider.Delete(ctx, retrievedNodeClaim)).To(Succeed())
		Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

		//// List should return no nodeclaims
		azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
		azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
		nodeClaims, err = cloudProvider.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1)) // Expect to be called
		Expect(nodeClaims).To(BeEmpty())

		//// Get should return NodeClaimNotFound error
		azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
		azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
		nodeClaim, err = cloudProvider.Get(ctx, createdNodeClaim.Status.ProviderID)
		Expect(err).To(HaveOccurred())
		Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered
		Expect(nodeClaim).To(BeNil())
	})

	// XPMT: TODO(comtalyst): deep inspection test on simulating all of these?
	Context("Unexpected API Failures", func() {
		It("should handle AKS machine create failures - unrecognized error during sync/initial", func() {
			// Set up error to occur immediately during BeginCreateOrUpdate call
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			// Verify the create API was called but failed
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			// Verify the cleanup was attempted
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

			// Clear the error for cleanup
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(nil)

			// Verify the pod is now schedulable
			pod2 := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod2)
			ExpectScheduled(ctx, env.Client, pod2)
		})

		It("should handle AKS machine create failures - unrecognized error during async/LRO", func() {
			// Set up error to occur during LRO polling (async failure)
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAny()

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			// Verify the create API was called but failed
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			// Verify the cleanup was attempted
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

			// Clear the error for cleanup
			azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil

			// Verify the pod is now schedulable
			pod2 := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod2)
			ExpectScheduled(ctx, env.Client, pod2)
		})

		It("should handle AKS machine get failures - unrecognized error", func() {
			// First create a successful AKS machine
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			validateAKSMachineNodeClaim(nodeClaims[0], nodePool)

			// Set up Get to fail
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

			// Attempt to get the nodeclaim - should fail
			retrievedNodeClaim, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
			Expect(err).To(HaveOccurred())
			Expect(retrievedNodeClaim).To(BeNil())
			// Verify the get API was called
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

			// Clear the error for cleanup
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)
		})

		It("should handle malformed timestamp tags gracefully during List operation", func() {
			// Create AKS machine with malformed timestamp tag directly in store
			opts := options.FromContext(ctx)
			aksMachine := test.AKSMachine(test.AKSMachineOptions{
				Name:             "malformed-timestamp-machine",
				MachinesPoolName: opts.AKSMachinesPoolName,
				ClusterName:      opts.ClusterName,
			})
			// Set malformed timestamp tag
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr("invalid-timestamp-format")
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			// List should not fail despite malformed timestamp
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nodeClaims)).To(BeNumerically(">=", 1))

			// Find our machine in the results
			var ourNodeClaim *karpv1.NodeClaim
			for _, nc := range nodeClaims {
				if nc.Annotations[v1beta1.AnnotationAKSMachineResourceID] == lo.FromPtr(aksMachine.ID) {
					ourNodeClaim = nc
					break
				}
			}
			Expect(ourNodeClaim).ToNot(BeNil())

			// CreationTimestamp should be zero due to parsing failure
			Expect(ourNodeClaim.CreationTimestamp.IsZero()).To(BeTrue())
		})

		It("should handle AKS machine delete failures - unrecognized error during sync/initial", func() {
			// First create a successful AKS machine
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			validateAKSMachineNodeClaim(nodeClaims[0], nodePool)

			// Set up delete to fail
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)

			// Attempt to delete the nodeclaim - should fail
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).To(HaveOccurred())
			// Verify the delete API was called
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

			// Clear the error for cleanup
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(nil)
		})

		It("should handle AKS machine delete failures - unrecognized error during async/LRO", func() {
			// First create a successful AKS machine
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			validateAKSMachineNodeClaim(nodeClaims[0], nodePool)

			// Set up delete to fail
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

			// Attempt to delete the nodeclaim - should fail
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).To(HaveOccurred())
			// Verify the delete API was called
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

			// Clear the error for cleanup
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.Error.Set(nil)
		})

		It("should handle AKS machine list failures - unrecognized error", func() {
			// Set up error to occur during the NextPage call
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Verify the list API was called but failed
			azureEnv.AKSAgentPoolsAPI.AgentPoolGetBehavior.CalledWithInput.Reset()
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).To(HaveOccurred())
			Expect(nodeClaims).To(BeEmpty())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

			// Clear the error for cleanup
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(nil)

			// Verify the pod is now schedulable
			pod2 := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod2)
			ExpectScheduled(ctx, env.Client, pod2)
		})
	})

	Context("Operation Conflicts/Races", func() {
		It("should handle AKS machine get/delete failures - not found/already deleted externally", func() {
			// First create a successful AKS machine
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			validateAKSMachineNodeClaim(nodeClaims[0], nodePool)

			// Delete the machine directly
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).ToNot(HaveOccurred())

			// Get should return NodeClaimNotFound error
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			retrievedNodeClaim2, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
			Expect(err).To(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
			Expect(retrievedNodeClaim2).To(BeNil())

			// Delete should also return NodeClaimNotFound error
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).To(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0)) // Per current logic, get should be called before delete
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

			// Clear the error for cleanup
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)
		})

		// Note: currently, we do not support different offerings requirements for the NodeClaim with the same name that attempted creation recently. The same applies with VM-based provisioning.
		It("should handle AKS machine create - found in get, with the same requirements", func() {
			// Create a fresh nodeClaim with explicit requirements so we know exactly what it will have
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
						{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{
								Key:      v1.LabelTopologyZone,
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")},
							},
						},
						{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{
								Key:      v1.LabelInstanceTypeStable,
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{"Standard_D2_v2"},
							},
						},
					},
				},
			})

			// First create a successful AKS machine using cloudProvider.Create directly
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
			createdFirstNodeClaim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, firstNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdFirstNodeClaim).ToNot(BeNil())
			validateAKSMachineNodeClaim(createdFirstNodeClaim, nodePool)
			Expect(createdFirstNodeClaim.CreationTimestamp).ToNot(BeZero())

			// Create a conflicted nodeclaim with same configuration
			conflictedNodeClaim := firstNodeClaim.DeepCopy()

			// Call cloudProvider.Create directly with the unconflicted nodeclaim to trigger get
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			nodeClaim, err = CreateAndDrain(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())

			// Verify the AKS machine was reused successfully
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))

			// Since no new machine was created, get the machine that was retrieved via Get
			getInput := azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Pop()
			aksMachineName := getInput.AKSMachineName

			// Get the actual machine from the fake store
			machineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, aksMachineName)
			existingMachine, ok := azureEnv.AKSDataStorage.AKSMachines.Load(machineID)
			Expect(ok).To(BeTrue(), "AKS machine should exist in fake store")
			aksMachine := existingMachine.(armcontainerservice.Machine)
			Expect(aksMachine.Properties).ToNot(BeNil())

			// Validate AKS machine properties match the conflicted configuration
			Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
			Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
			Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal("Standard_D2_v2"))
			Expect(aksMachine.Zones).To(HaveLen(1))
			Expect(*aksMachine.Zones[0]).To(Equal("1"))

			// Validate the returned nodeClaim has correct configuration
			validateAKSMachineNodeClaim(nodeClaim, nodePool)
			Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")))
			Expect(nodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})

		It("should handle AKS machine create failures - not found in get, but somehow found during create, although with same configuration", func() {
			// Create a fresh nodeClaim with explicit requirements so we know exactly what it will have
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
						{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{
								Key:      v1.LabelTopologyZone,
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")},
							},
						},
						{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{
								Key:      v1.LabelInstanceTypeStable,
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{"Standard_D2_v2"},
							},
						},
					},
				},
			})

			// First create a successful AKS machine using cloudProvider.Create directly
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
			createdFirstNodeClaim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, firstNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdFirstNodeClaim).ToNot(BeNil())
			validateAKSMachineNodeClaim(createdFirstNodeClaim, nodePool)
			Expect(createdFirstNodeClaim.CreationTimestamp).ToNot(BeZero())

			// Create a conflicted nodeclaim with same configuration
			conflictedNodeClaim := firstNodeClaim.DeepCopy()

			// Simulate Get being faulty (or the previous machine comes into exist between get and create)
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorFromAKSMachineNotFound)

			// Call cloudProvider.Create directly with the unconflicted nodeclaim to trigger empty create
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			nodeClaim, err = CreateAndDrain(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaim).ToNot(BeNil())

			// Verify the AKS machine was created successfully
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			aksMachine := createInput.AKSMachine
			Expect(aksMachine.Properties).ToNot(BeNil())

			// Validate AKS machine properties match the conflicted configuration
			Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
			Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
			Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal("Standard_D2_v2"))
			Expect(aksMachine.Zones).To(HaveLen(1))
			Expect(*aksMachine.Zones[0]).To(Equal("1"))

			// Validate the returned nodeClaim has correct configuration
			validateAKSMachineNodeClaim(nodeClaim, nodePool)
			Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")))
			Expect(nodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})

		It("should handle AKS machine create failures - not found in get, but somehow found during create, although with conflicted configuration", func() {
			// Create a fresh nodeClaim with explicit requirements so we know exactly what it will have
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
						{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{
								Key:      v1.LabelTopologyZone,
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{utils.MakeAKSLabelZoneFromARMZone(fake.Region, "1")},
							},
						},
						{
							NodeSelectorRequirement: v1.NodeSelectorRequirement{
								Key:      v1.LabelInstanceTypeStable,
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{"Standard_D2_v2"},
							},
						},
					},
				},
			})

			// First create a successful AKS machine using cloudProvider.Create directly
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
			createdFirstNodeClaim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, firstNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdFirstNodeClaim).ToNot(BeNil())
			validateAKSMachineNodeClaim(createdFirstNodeClaim, nodePool)
			Expect(createdFirstNodeClaim.CreationTimestamp).ToNot(BeZero())

			// Create a conflicted nodeclaim with different immutable configuration (zone/SKU)
			conflictedNodeClaim := firstNodeClaim.DeepCopy()
			// Change zone to create immutable configuration conflict
			conflictedNodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelTopologyZone,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{utils.MakeAKSLabelZoneFromARMZone(fake.Region, "2")}, // Different zone
					},
				},
				{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v5"}, // Different SKU
					},
				},
			}

			// Simulate Get being faulty (or the previous machine comes into exist between get and create)
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorFromAKSMachineNotFound)

			// Call cloudProvider.Create directly with the conflicted nodeclaim to trigger the race condition
			// This targets the same machine name but should fail due to configuration conflict and trigger cleanup
			_, err = CreateAndDrain(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).To(HaveOccurred())

			// Verify cleanup was attempted after the conflict
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

			// Clear the error
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)

			// Should succeed now that the conflicted node is gone from the cleanup
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			createdConflictedNodeClaim, err := CreateAndDrain(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdConflictedNodeClaim).ToNot(BeNil())

			// Verify the AKS machine was created successfully
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			aksMachine := createInput.AKSMachine
			Expect(aksMachine.Properties).ToNot(BeNil())

			// Validate AKS machine properties match the conflicted configuration
			Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
			Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
			Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal("Standard_D2_v5"))
			Expect(aksMachine.Zones).To(HaveLen(1))
			Expect(*aksMachine.Zones[0]).To(Equal("2"))

			// Validate the returned nodeClaim has correct configuration
			validateAKSMachineNodeClaim(createdConflictedNodeClaim, nodePool)
			Expect(createdConflictedNodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(utils.MakeAKSLabelZoneFromARMZone(fake.Region, "2")))
			Expect(createdConflictedNodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v5"))
			Expect(createdConflictedNodeClaim.CreationTimestamp).ToNot(BeZero())
		})
	})
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = AKSMachineAPI, ManageExistingAKSMachines = false", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				UseSIG:                    lo.ToPtr(true),
				ManageExistingAKSMachines: lo.ToPtr(false), // should not have any effect, as ProvisionMode is AKSMachineAPI
			})
			// Enable batch creation to test batch client + GET poller
			testOptions.BatchCreationEnabled = true
			testOptions.BatchIdleTimeoutMS = 100
			testOptions.BatchMaxTimeoutMS = 1000
			testOptions.MaxBatchSize = 50

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
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset()
			azureEnvNonZonal.Reset()
		})

		// Run shared AKS Machine API tests
		runSharedAKSMachineAPITests()
	})

	Context("ProvisionMode = AKSMachineAPI, ManageExistingAKSMachines = true", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				UseSIG:                    lo.ToPtr(true),
				ManageExistingAKSMachines: lo.ToPtr(true), // should not have any effect
			})
			// Enable batch creation to test batch client + GET poller
			testOptions.BatchCreationEnabled = true
			testOptions.BatchIdleTimeoutMS = 100
			testOptions.BatchMaxTimeoutMS = 1000
			testOptions.MaxBatchSize = 50

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
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset()
			azureEnvNonZonal.Reset()
		})

		// Run shared AKS Machine API tests
		runSharedAKSMachineAPITests()
	})

	Context("Mixed Environment - Migration from ProvisionMode = AKSMachineAPI to VM mode", func() {
		Context("ManageExistingAKSMachines = false", func() {
			var existingAKSMachine *armcontainerservice.Machine

			BeforeEach(func() {
				testOptions = test.Options(test.OptionsFields{
					ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless), // Switch to VM mode
					UseSIG:                    lo.ToPtr(true),
					ManageExistingAKSMachines: lo.ToPtr(false), // Disable AKS machines management
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

				// Create an existing AKS machine to simulate the migration scenario
				agentPool := test.AKSAgentPool(test.AKSAgentPoolOptions{
					Name:          testOptions.AKSMachinesPoolName,
					ResourceGroup: testOptions.NodeResourceGroup,
					ClusterName:   testOptions.ClusterName,
				})
				azureEnv.AKSDataStorage.AgentPools.Store(lo.FromPtr(agentPool.ID), *agentPool)

				existingAKSMachine = test.AKSMachine(test.AKSMachineOptions{
					Name:             "existing-aks-machine",
					MachinesPoolName: testOptions.AKSMachinesPoolName,
					ClusterName:      testOptions.ClusterName,
				})
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(existingAKSMachine.ID), *existingAKSMachine)
			})

			AfterEach(func() {
				// Wait for any async polling goroutines to complete before resetting
				cloudProvider.WaitForInstancePromises()
				cluster.Reset()
				azureEnv.Reset()
			})

			It("should handle basic operations - new nodes use VMs, existing AKS machines are still visible", func() {
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)

				// Scale-up 1 new node - should create VM, not AKS machine
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "test-migration-vm-mgmt"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test-migration-vm-mgmt"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
				})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Should call VM APIs instead of AKS Machine APIs for new nodes
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(createInput.VM.Properties).ToNot(BeNil())

				// List should return VM nodeclaims only
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(0)) // Should be intercepted
				Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))

				// Should VM nodeclaim only
				Expect(nodeClaims).To(HaveLen(1))
				vmNodeClaim := nodeClaims[0]
				validateVMNodeClaim(vmNodeClaim, nodePool)

				// Get should not return AKS machine nodeclaim
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
				_, err = cloudProvider.Get(ctx, utils.VMResourceIDToProviderID(ctx, *existingAKSMachine.Properties.ResourceID))
				Expect(err).To(HaveOccurred())
				// Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(0))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

				// Get should return VM nodeclaim
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
				retrievedVMNodeClaim, err := cloudProvider.Get(ctx, vmNodeClaim.Status.ProviderID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered given the name is fine
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))

				// The returned nodeClaim should be correct
				Expect(retrievedVMNodeClaim).ToNot(BeNil())
				Expect(retrievedVMNodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSMachineResourceID))

				// Delete VM nodeclaim
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
				Expect(cloudProvider.Delete(ctx, vmNodeClaim)).To(Succeed())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))

				// List should return no nodeclaims
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
				nodeClaims, err = cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(0)) // Should be intercepted
				Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(nodeClaims).To(BeEmpty())
			})
		})

		Context("ManageExistingAKSMachines = true", func() {
			var existingAKSMachine *armcontainerservice.Machine

			BeforeEach(func() {
				testOptions = test.Options(test.OptionsFields{
					ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless), // Switch to VM mode
					UseSIG:                    lo.ToPtr(true),
					ManageExistingAKSMachines: lo.ToPtr(true), // Enable AKS machines management
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

				// Create an existing AKS machine to simulate the migration scenario

				agentPool := test.AKSAgentPool(test.AKSAgentPoolOptions{
					Name:          testOptions.AKSMachinesPoolName,
					ResourceGroup: testOptions.NodeResourceGroup,
					ClusterName:   testOptions.ClusterName,
				})
				azureEnv.AKSDataStorage.AgentPools.Store(lo.FromPtr(agentPool.ID), *agentPool)

				existingAKSMachine = test.AKSMachine(test.AKSMachineOptions{
					Name:             "existing-aks-machine-mgmt",
					MachinesPoolName: testOptions.AKSMachinesPoolName,
					ClusterName:      testOptions.ClusterName,
				})
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(existingAKSMachine.ID), *existingAKSMachine)
			})

			AfterEach(func() {
				// Wait for any async polling goroutines to complete before resetting
				cloudProvider.WaitForInstancePromises()
				cluster.Reset()
				azureEnv.Reset()
			})

			It("should handle basic operations - new nodes use VMs, existing AKS machines are still visible", func() {
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)

				// Scale-up 1 new node - should create VM, not AKS machine
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "test-migration-vm-mgmt"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test-migration-vm-mgmt"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
				})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Should call VM APIs instead of AKS Machine APIs for new nodes
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(createInput.VM.Properties).ToNot(BeNil())

				// List should return both VM and AKS machine nodeclaims
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))

				// Should return both VM and AKS machine nodeclaims
				Expect(nodeClaims).To(HaveLen(2))
				var aksMachineNodeClaim *karpv1.NodeClaim
				var vmNodeClaim *karpv1.NodeClaim
				if nodeClaims[0].Annotations[v1beta1.AnnotationAKSMachineResourceID] != "" {
					aksMachineNodeClaim = nodeClaims[0]
					vmNodeClaim = nodeClaims[1]
				} else {
					vmNodeClaim = nodeClaims[0]
					aksMachineNodeClaim = nodeClaims[1]
				}
				// validateAKSMachineNodeClaim(aksMachineNodeClaim, nodePool)  // Not covered as this fake VM does not have enough data in the first place
				validateVMNodeClaim(vmNodeClaim, nodePool)

				// Get should return AKS machine nodeclaim
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
				retrievedAKSNodeClaim, err := cloudProvider.Get(ctx, aksMachineNodeClaim.Status.ProviderID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

				// The returned nodeClaim should be correct
				Expect(retrievedAKSNodeClaim).ToNot(BeNil())
				// Expect(retrievedAKSNodeClaim.Status.Capacity).ToNot(BeEmpty()) // Not covered as this fake VM does not have enough data in the first place
				Expect(retrievedAKSNodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
				Expect(retrievedAKSNodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]).ToNot(BeEmpty())

				// Get should return VM nodeclaim
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
				retrievedVMNodeClaim, err := cloudProvider.Get(ctx, vmNodeClaim.Status.ProviderID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered given the name is fine
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))

				// The returned nodeClaim should be correct
				Expect(retrievedVMNodeClaim).ToNot(BeNil())
				Expect(retrievedVMNodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSMachineResourceID))

				// Delete AKS machine nodeclaim
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
				Expect(cloudProvider.Delete(ctx, aksMachineNodeClaim)).To(Succeed())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(0))

				// Delete VM nodeclaim
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
				Expect(cloudProvider.Delete(ctx, vmNodeClaim)).To(Succeed())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))

				// List should return no nodeclaims
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
				nodeClaims, err = cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(nodeClaims).To(BeEmpty()) // Both should be deleted
			})

			Context("Unexpected API Failures with Existing AKS Machines", func() {
				BeforeEach(func() {
					// Ensure we have an existing AKS machine in the environment for failure testing
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				})

				It("should handle AKS machine list failures - unrecognized error", func() {
					// Set up AKS Machine List to fail
					azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

					// List should return error when AKS machine API fails, even though we're in VM mode
					allNodeClaims, err := cloudProvider.List(ctx)
					Expect(err).To(HaveOccurred())
					Expect(allNodeClaims).To(BeEmpty())

					// Clear the error for cleanup
					azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(nil)
				})

				It("should handle AKS machine get failures - unrecognized error", func() {
					// Get the AKS machine nodeclaim for testing
					nodeClaims, err := cloudProvider.List(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(nodeClaims).To(HaveLen(1))
					// validateAKSMachineNodeClaim(nodeClaims[0], nodePool)  // Not covered as this fake VM does not have enough data in the first place

					// Set up Get to fail
					azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

					// Attempt to get the AKS machine nodeclaim - should fail
					retrievedNodeClaim, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
					Expect(err).To(HaveOccurred())
					Expect(retrievedNodeClaim).To(BeNil())
					// Verify the get API was called
					Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

					// Clear the error for cleanup
					azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)
				})

				It("should handle AKS machine delete failures - unrecognized error", func() {
					// Get the AKS machine nodeclaim for testing
					nodeClaims, err := cloudProvider.List(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(nodeClaims).To(HaveLen(1))
					// validateAKSMachineNodeClaim(nodeClaims[0], nodePool)  // Not covered as this fake VM does not have enough data in the first place

					// Set up delete to fail
					azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)

					// Attempt to delete the AKS machine nodeclaim - should fail
					err = cloudProvider.Delete(ctx, nodeClaims[0])
					Expect(err).To(HaveOccurred())
					// Verify the delete API was called
					Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

					// Clear the error for cleanup
					azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(nil)
				})
			})
		})
	})

	Context("Mixed Environment - Migration to ProvisionMode = AKSMachineAPI", func() {
		var existingVM *armcompute.VirtualMachine

		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				UseSIG:        lo.ToPtr(true),
			})
			// Enable batch creation to test batch client + GET poller
			testOptions.BatchCreationEnabled = true
			testOptions.BatchIdleTimeoutMS = 100
			testOptions.BatchMaxTimeoutMS = 1000
			testOptions.MaxBatchSize = 50

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

			existingVM = test.VirtualMachine()
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(existingVM.ID), *existingVM)
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset()
		})

		It("should be able to handle basic operations", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)

			// Scale-up 1 node
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			pod := coretest.UnschedulablePod(coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test-migration"},
				},
				PodAntiRequirements: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "test-migration"},
						},
						TopologyKey: v1.LabelHostname,
					},
				},
			})
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			//// Should call AKS Machine APIs instead of VM APIs
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			Expect(createInput.AKSMachine.Properties).ToNot(BeNil())

			// List should return both VM and AKS machine nodeclaims
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
			azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))

			//// Validate if they are correct
			Expect(nodeClaims).To(HaveLen(2))
			var aksMachineNodeClaim *karpv1.NodeClaim
			var vmNodeClaim *karpv1.NodeClaim
			if nodeClaims[0].Annotations[v1beta1.AnnotationAKSMachineResourceID] != "" {
				aksMachineNodeClaim = nodeClaims[0]
				vmNodeClaim = nodeClaims[1]
			} else {
				vmNodeClaim = nodeClaims[0]
				aksMachineNodeClaim = nodeClaims[1]
			}
			validateAKSMachineNodeClaim(aksMachineNodeClaim, nodePool)

			// validateVMNodeClaim(vmNodeClaim, nodePool) // Not covered as this fake VM does not have enough data in the first place
			Expect(vmNodeClaim.Status.ProviderID).To(Equal(utils.VMResourceIDToProviderID(ctx, *existingVM.ID)))

			// Get should return AKS machine nodeclaim
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			retrievedAKSNodeClaim, err := cloudProvider.Get(ctx, aksMachineNodeClaim.Status.ProviderID)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

			//// The returned nodeClaim should be correct
			Expect(retrievedAKSNodeClaim).ToNot(BeNil())
			Expect(retrievedAKSNodeClaim.Status.Capacity).ToNot(BeEmpty())
			Expect(retrievedAKSNodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
			Expect(retrievedAKSNodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]).ToNot(BeEmpty())

			// Get should return VM nodeclaim
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			nodeClaim, err = cloudProvider.Get(ctx, vmNodeClaim.Status.ProviderID)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered given the name is fine
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))

			//// The returned nodeClaim should be correct
			Expect(nodeClaim).ToNot(BeNil())
			Expect(*existingVM.Name).To(ContainSubstring(nodeClaim.Name))
			Expect(nodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSMachineResourceID))

			// Delete VM nodeclaim
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
			Expect(cloudProvider.Delete(ctx, vmNodeClaim)).To(Succeed())
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))

			//// List should return no nodeclaims
			azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Reset()
			azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Reset()
			nodeClaims, err = cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(nodeClaims).To(HaveLen(1)) // Only AKS machine nodeclaim should remain

			//// Get AKS machine should still found
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			nodeClaim, err = cloudProvider.Get(ctx, aksMachineNodeClaim.Status.ProviderID)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered
			Expect(nodeClaim).ToNot(BeNil())
			validateAKSMachineNodeClaim(nodeClaim, nodePool)

			//// Get VM nodeClaim should return NodeClaimNotFound error
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			nodeClaim, err = cloudProvider.Get(ctx, vmNodeClaim.Status.ProviderID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(nodeClaim).To(BeNil())
		})

		Context("Unexpected API Failures", func() {
			BeforeEach(func() {
				// Scale-up 1 AKS machine node
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "test-migration"},
					},
					PodAntiRequirements: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test-migration"},
							},
							TopologyKey: v1.LabelHostname,
						},
					},
				})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})
			It("should handle VM list (ARG) failures - unrecognized error", func() {
				// Set up Resource Graph to fail
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.Error.Set(
					&azcore.ResponseError{
						ErrorCode: "SomeRandomError",
					},
				)

				// List should return error when either error occurs
				allNodeClaims, err := cloudProvider.List(ctx)
				Expect(err).To(HaveOccurred())
				Expect(allNodeClaims).To(BeEmpty())
				// Clear the error for cleanup
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.Error.Set(nil)
			})

			It("should handle AKS machine list failurse - unrecognized error", func() {
				// Set up AKS Machine List to fail
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

				// List should return error when either error occurs
				allNodeClaims, err := cloudProvider.List(ctx)
				Expect(err).To(HaveOccurred())
				Expect(allNodeClaims).To(BeEmpty())

				// Clear the error for cleanup
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(nil)
			})
		})

		Context("AKS Machines Pool Management", func() {
			It("should handle AKS machines pool not found on each CloudProvider operation", func() {
				// First create a successful AKS machine
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Get the created nodeclaim
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(2))
				var aksMachineNodeClaim *karpv1.NodeClaim
				if nodeClaims[0].Annotations[v1beta1.AnnotationAKSMachineResourceID] != "" {
					aksMachineNodeClaim = nodeClaims[0]
				} else {
					aksMachineNodeClaim = nodeClaims[1]
				}
				validateAKSMachineNodeClaim(aksMachineNodeClaim, nodePool)
				aksMachineNodeClaim.Spec.NodeClassRef = &karpv1.NodeClassReference{ // Normally core would do this.
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				}

				// Delete the AKS machines pool from the record
				agentPoolID := fake.MkAgentPoolID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName)
				azureEnv.AKSDataStorage.AgentPools.Delete(agentPoolID)
				// (then, mostly relying on fake API to reflect the correct behavior)

				// cloudprovider.Get should return NodeClaimNotFoundError, but not panic
				retrievedNodeClaim3, err := cloudProvider.Get(ctx, aksMachineNodeClaim.Status.ProviderID)
				Expect(err).To(HaveOccurred())
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(retrievedNodeClaim3).To(BeNil())

				// cloudprovider.List should return vms only
				nodeClaims, err = cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))

				// cloudprovider.Delete should return NodeClaimNotFoundError, but not panic
				err = cloudProvider.Delete(ctx, aksMachineNodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

				// cloudprovider.Create should panic
				Expect(func() {
					_, _ = cloudProvider.Create(ctx, retrievedNodeClaim3)
				}).To(Panic())
			})
		})

	})

	// --- AKSScriptless mode tests (VM-based provisioning) ---
	// These tests verify behavior when ProvisionMode = AKSScriptless.
	// If ProvisionMode = AKSScriptless is no longer supported, these tests will be removed.

	// runSharedAKSScriptlessTests generates the common tests for AKSScriptless provisioning mode.
	// expectedListPagerCalls varies by ManageExistingAKSMachines setting (0 when false, 1 when true).
	runSharedAKSScriptlessTests := func(expectedListPagerCalls int) {
		It("should list nodeclaim created by the CloudProvider", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			nodeClaims, _ := cloudProvider.List(ctx)
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(expectedListPagerCalls))
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
			queryRequest := azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Pop().Query
			Expect(*queryRequest.Query).To(Equal(instance.GetVMListQueryBuilder(azureEnv.AzureResourceGraphAPI.ResourceGroup).String()))
			Expect(nodeClaims).To(HaveLen(1))
			validateVMNodeClaim(nodeClaims[0], nodePool)
			resp, _ := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, nodeClaims[0].Name, nil)
			Expect(resp.VirtualMachine).ToNot(BeNil())
		})

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

		Context("AKS Machine API integration", func() {
			It("should not call writes to AKS Machine API", func() {
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			Context("AKS Machines Pool Management", func() {
				It("should handle AKS machines pool not found on each CloudProvider operation", func() {
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)

					nodeClaims, err := cloudProvider.List(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(nodeClaims).To(HaveLen(1))
					validateVMNodeClaim(nodeClaims[0], nodePool)

					err = cloudProvider.Delete(ctx, nodeClaims[0])
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})
	}

	setupAKSScriptless := func(manageExisting bool) {
		testOptions = test.Options(test.OptionsFields{
			ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless),
			ManageExistingAKSMachines: lo.ToPtr(manageExisting),
		})
		ctx = coreoptions.ToContext(ctx, coretest.Options())
		ctx = options.ToContext(ctx, testOptions)

		azureEnv = test.NewEnvironment(ctx, env)
		test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
		cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)

		cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
		coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
	}

	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = false", func() {
		BeforeEach(func() { setupAKSScriptless(false) })
		AfterEach(func() {
			cluster.Reset()
			azureEnv.Reset()
		})
		runSharedAKSScriptlessTests(0) // ManageExisting=false: no list pager calls
	})

	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = true", func() {
		BeforeEach(func() { setupAKSScriptless(true) })
		AfterEach(func() {
			cluster.Reset()
			azureEnv.Reset()
		})
		runSharedAKSScriptlessTests(1) // ManageExisting=true: expects list pager call
	})
})
