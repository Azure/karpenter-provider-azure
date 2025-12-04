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

package garbagecollection_test

import (
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
)

var _ = Describe("Instance Garbage Collection", func() {
	var vm *armcompute.VirtualMachine
	var aksMachine *armcontainerservice.Machine
	var providerID string
	var err error

	var _ = Context("AKS machine instances", func() {
		BeforeEach(func() {
			// Assume that AKS machines pool exists at this point.
			// Retrieve parameters from context to match the exact parameters used by the AKS machine provider
			opts := options.FromContext(ctx)
			agentPool := test.AKSAgentPool(test.AKSAgentPoolOptions{
				Name:          opts.AKSMachinesPoolName, // From context
				ResourceGroup: opts.NodeResourceGroup,   // From context
				ClusterName:   opts.ClusterName,         // From context
			})
			azureEnv.AKSDataStorage.AgentPools.Store(lo.FromPtr(agentPool.ID), *agentPool)

			aksMachine = test.AKSMachine(test.AKSMachineOptions{Name: "aks-machine-a", MachinesPoolName: opts.AKSMachinesPoolName})
			providerID = utils.VMResourceIDToProviderID(ctx, lo.FromPtr(aksMachine.Properties.ResourceID))
		})

		It("should delete an AKS machine if there is no NodeClaim owner", func() {
			// Launch happened 10m ago
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})

		It("should delete an AKS machine if there is no NodeClaim owner, and with malformed timestamp tag", func() {
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr("malformed-timestamp")
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)

			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})

		It("should delete an AKS machine if there is no NodeClaim owner, and without timestamp tag", func() {
			delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)

			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})

		It("should not delete an AKS machine if there is no NodeClaim owner, but was not launched by a NodeClaim", func() {
			// Remove the managed-by tag (this isn't launched by a NodeClaim)
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
			aksMachine.Properties.Tags = lo.OmitBy(aksMachine.Properties.Tags, func(key string, value *string) bool {
				return key == launchtemplate.NodePoolTagKey
			})
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not delete an AKS machine if there is no NodeClaim owner, but within the nodeClaim resolution window (5m)", func() {
			// Launch time just happened
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp()))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not delete the AKS machine or node if it already has a nodeClaim that matches it", func() {
			// Launch time was 10m ago
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				Status: karpv1.NodeClaimStatus{
					ProviderID: providerID,
				},
			})
			node := coretest.Node(coretest.NodeOptions{
				ProviderID: providerID,
			})
			ExpectApplied(ctx, env.Client, nodeClaim, node)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).ToNot(HaveOccurred())
			ExpectExists(ctx, env.Client, node)
		})

		It("should delete an AKS machine along with the node if there is no NodeClaim owner (to quicken scheduling)", func() {
			// Launch happened 10m ago
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
			node := coretest.Node(coretest.NodeOptions{
				ProviderID: providerID,
			})
			ExpectApplied(ctx, env.Client, node)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

			ExpectNotFound(ctx, env.Client, node)
		})

		var _ = Context("Complex tags manipulation scenarios with in-place updates", func() {
			var nodeClaim *karpv1.NodeClaim
			var nodeClass *v1beta1.AKSNodeClass
			var providerID string

			BeforeEach(func() {
				// Set up agent pool for AKS machines
				opts := options.FromContext(ctx)
				agentPool := test.AKSAgentPool(test.AKSAgentPoolOptions{
					Name:          opts.AKSMachinesPoolName,
					ResourceGroup: opts.NodeResourceGroup,
					ClusterName:   opts.ClusterName,
				})
				azureEnv.AKSDataStorage.AgentPools.Store(lo.FromPtr(agentPool.ID), *agentPool)

				// Create AKS machine
				aksMachine = test.AKSMachine(test.AKSMachineOptions{Name: "corner-case-machine", MachinesPoolName: opts.AKSMachinesPoolName})
				providerID = utils.VMResourceIDToProviderID(ctx, lo.FromPtr(aksMachine.Properties.ResourceID))

				// Create corresponding NodeClaim, not launched yet
				nodeClass = test.AKSNodeClass()
				nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name: "corner-case-nodeclaim",
						Annotations: map[string]string{
							v1beta1.AnnotationAKSMachineResourceID: lo.FromPtr(aksMachine.ID),
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

			AfterEach(func() {
				ExpectCleanedUp(ctx, env.Client)
			})

			It("Instance created -> Tag deleted -> In-place update -> Garbage collection false positive -> Create() completed -> Registered -> In-place update", func() {
				// Blank NodeClaim is there from the core.
				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)

				// AKS machine created, but the user somehow deleted the timestamp tag
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_nodeclaim")
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

				// Provider still waiting for Create() to complete. No change to NodeClaim.

				// In-place update reconciles - should not do anything as no ProviderID yet
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)
				// Verify no update calls and the timestamp tag stays broken.
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				unchangedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
				Expect(err).ToNot(HaveOccurred())
				Expect(unchangedAKSMachine.Properties.Tags).ToNot(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
				Expect(unchangedAKSMachine.Properties.Tags).ToNot(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))

				// Garbage collection reconciles - should garbage collect due to no NodeClaim owner + timestamp defaulting to epoch
				// This is expected, but not what we really wanted... See suggestions in respective modules.
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).To(HaveOccurred())
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

				// Provider Create() completes, setting the ProviderID on the NodeClaim
				// Assume this comes at unfortunate time and just went in effect...
				nodeClaim.Status.ProviderID = providerID
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// NodeClaim gets registered
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// In-place update reconciles again - should error NodeClaim not found, as instance is gone
				_, err = inPlaceUpdateController.Reconcile(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				// Verify no additional update calls and no instance
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				_, err = azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
				Expect(err).To(HaveOccurred()) // Still gone
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

				// Core will eventually clean up the orphaned NodeClaim
			})

			It("Instance created -> Tag deleted -> Create() completed -> Registered -> In-place update -> Garbage collection negative -> In-place update -> Garbage collection negative -> Tag deleted -> Garbage collection negative", func() {
				// Blank NodeClaim is there from the core.
				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)

				// AKS machine created, but the user somehow deleted the timestamp tag
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_nodeclaim")
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

				// Provider Create() completes, setting the ProviderID on the NodeClaim
				nodeClaim.Status.ProviderID = providerID
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// NodeClaim gets registered
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// In-place update reconciles - should repair timestamp tag to epoch
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)
				// Verify update call and the timestamp tag is repaired to epoch.
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
				Expect(err).ToNot(HaveOccurred())
				Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
				Expect(*updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"]).To(Equal(instance.AKSMachineTimestampToTag(instance.ZeroAKSMachineTimestamp())))
				Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
				Expect(*updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_nodeclaim"]).To(Equal("corner-case-nodeclaim"))

				// Garbage collection reconciles - should not garbage collect due to NodeClaim owner
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))

				// In-place update reconciles again - should preserve existing timestamp tag
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)
				// Verify no additional update calls and the timestamp tag stays unchanged.
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				unchangedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
				Expect(err).ToNot(HaveOccurred())
				Expect(unchangedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
				Expect(*unchangedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"]).To(Equal(instance.AKSMachineTimestampToTag(instance.ZeroAKSMachineTimestamp())))
				Expect(unchangedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
				Expect(*unchangedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_nodeclaim"]).To(Equal("corner-case-nodeclaim"))

				// Garbage collection reconciles - should not garbage collect due to NodeClaim owner
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))

				// The user somehow deleted the timestamp tag again
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_nodeclaim")
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

				// Garbage collection reconciles - should not garbage collect due to NodeClaim owner
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			It("Instance created -> Tag deleted -> In-place update -> Create() completed -> Registered -> Garbage collection negative", func() {
				// Blank NodeClaim is there from the core.
				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)

				// AKS machine created, but the user somehow deleted the timestamp tag
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_nodeclaim")
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

				// In-place update reconciles - should not do anything as no ProviderID yet
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)
				// Verify no update calls and the timestamp tag stays broken.
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				unchangedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
				Expect(err).ToNot(HaveOccurred())
				Expect(unchangedAKSMachine.Properties.Tags).ToNot(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
				Expect(unchangedAKSMachine.Properties.Tags).ToNot(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))

				// Provider Create() completes, setting the ProviderID on the NodeClaim
				nodeClaim.Status.ProviderID = providerID
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// NodeClaim gets registered
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// Garbage collection reconciles - should not garbage collect due to NodeClaim owner
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			It("Instance created -> Tag deleted -> Create() completed -> Garbage collection negative", func() {
				// Blank NodeClaim is there from the core.
				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)

				// AKS machine created, but the user somehow deleted the timestamp tag
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_nodeclaim")
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

				// Provider Create() completes, setting the ProviderID on the NodeClaim
				nodeClaim.Status.ProviderID = providerID
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// Garbage collection reconciles - should not garbage collect due to NodeClaim owner
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
			})

			It("Instance created -> Tag deleted -> Create() completed -> Registered -> Garbage collection negative", func() {
				// Blank NodeClaim is there from the core.
				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)

				// AKS machine created, but the user somehow deleted the timestamp tag
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
				delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_nodeclaim")
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

				// Provider Create() completes, setting the ProviderID on the NodeClaim
				nodeClaim.Status.ProviderID = providerID
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// NodeClaim gets registered
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, nodeClaim)

				// Garbage collection reconciles - should not garbage collect due to NodeClaim owner
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
			})
		})
	})

	var _ = Context("Mixed VM and AKS machine instances", func() {
		BeforeEach(func() {
			// Set up agent pool for AKS machines in mixed tests
			opts := options.FromContext(ctx)
			agentPool := test.AKSAgentPool(test.AKSAgentPoolOptions{
				Name:          opts.AKSMachinesPoolName,
				ResourceGroup: opts.NodeResourceGroup,
				ClusterName:   opts.ClusterName,
			})
			azureEnv.AKSDataStorage.AgentPools.Store(lo.FromPtr(agentPool.ID), *agentPool)
		})

		It("should handle both VM and AKS machine instances in the same cluster", func() {
			// Create a VM instance without NodeClaim (should be deleted)
			vm = test.VirtualMachine(test.VirtualMachineOptions{
				Name:         "vm-mixed",
				NodepoolName: "default",
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)),
				},
			})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
			vmProviderID := utils.VMResourceIDToProviderID(ctx, lo.FromPtr(vm.ID))

			// Create an AKS machine instance without NodeClaim (should be deleted)
			opts := options.FromContext(ctx)
			aksMachine = test.AKSMachine(test.AKSMachineOptions{
				Name:             "aks-machine-mixed",
				MachinesPoolName: opts.AKSMachinesPoolName,
			})
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
			aksMachineProviderID := utils.VMResourceIDToProviderID(ctx, lo.FromPtr(aksMachine.Properties.ResourceID))

			ExpectSingletonReconciled(ctx, InstanceGCController)

			// Both instances should be deleted
			_, err := cloudProvider.Get(ctx, vmProviderID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

			_, err = cloudProvider.Get(ctx, aksMachineProviderID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})

		It("should preserve instances with NodeClaims and delete orphaned instances", func() {
			// Create a VM instance with NodeClaim (should be preserved)
			vm = test.VirtualMachine(test.VirtualMachineOptions{
				Name:         "vm-with-claim",
				NodepoolName: "default",
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)),
				},
			})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
			vmProviderID := utils.VMResourceIDToProviderID(ctx, lo.FromPtr(vm.ID))
			vmNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				Status: karpv1.NodeClaimStatus{
					ProviderID: vmProviderID,
				},
			})
			ExpectApplied(ctx, env.Client, vmNodeClaim)

			// Create an AKS machine instance without NodeClaim (should be deleted)
			opts := options.FromContext(ctx)
			aksMachine = test.AKSMachine(test.AKSMachineOptions{
				Name:             "aks-machine-orphaned",
				MachinesPoolName: opts.AKSMachinesPoolName,
			})
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
			aksMachineProviderID := utils.VMResourceIDToProviderID(ctx, lo.FromPtr(aksMachine.Properties.ResourceID))

			ExpectSingletonReconciled(ctx, InstanceGCController)

			// VM with NodeClaim should be preserved
			_, err := cloudProvider.Get(ctx, vmProviderID)
			Expect(err).ToNot(HaveOccurred())

			// AKS machine without NodeClaim should be deleted
			_, err = cloudProvider.Get(ctx, aksMachineProviderID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})
	})
})

var _ = Describe("NetworkInterface Garbage Collection", func() {

	// Note: this won't really test ARG query, which is the most important part of the flow. More like testing the fake of it.
	// Suggestion: find a way to effectively test ARG query that is not manual?
	var _ = Context("Mixed VM and AKS machine instances", func() {
		It("should not delete an untagged NIC if there is no associated VM", func() {
			nic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
				Tags:         map[string]*string{}, // untagged
			})
			nic2 := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			nic3 := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic2.ID), *nic2)
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic3.ID), *nic3)
			nicsBeforeGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsBeforeGC)).To(Equal(2))
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(0))
			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesDeleteBehavior.CalledWithInput.Len()).To(Equal(2))
		})

		It("should not delete an AKS Machine NIC if there is no associated VM", func() {
			nic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
				Tags:         test.ManagedTagsAKSMachine(nodePool.Name, "some-nodeclaim", instance.ZeroAKSMachineTimestamp()),
			})
			nic2 := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			nic3 := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic2.ID), *nic2)
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic3.ID), *nic3)
			nicsBeforeGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsBeforeGC)).To(Equal(2))
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(0))
			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesDeleteBehavior.CalledWithInput.Len()).To(Equal(2))
		})
	})
})
