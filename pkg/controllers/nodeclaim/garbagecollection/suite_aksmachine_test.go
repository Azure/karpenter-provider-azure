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
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
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

// gcTestMode provides mode-specific operations for shared garbage collection tests.
// Both VM and AKS machine contexts provide their own implementation, so the common
// test logic can run against either instance type without duplication.
type gcTestMode struct {
	storeInstanceOld func()        // Store instance created 10m ago (should be GC'd)
	storeInstanceNew func()        // Store instance just created (within resolution window)
	storeNoManagedBy func()        // Store old instance without managed-by tag
	getProviderID    func() string // Get the providerID of the current instance
}

// runSharedInstanceGCTests generates the common instance garbage collection tests
// that apply to both VM and AKS machine modes.
func runSharedInstanceGCTests(mode func() gcTestMode) {
	It("should delete an instance if there is no NodeClaim owner", func() {
		m := mode()
		m.storeInstanceOld()

		ExpectSingletonReconciled(ctx, InstanceGCController)
		_, err := cloudProvider.Get(ctx, m.getProviderID())
		Expect(err).To(HaveOccurred())
		Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
	})

	It("should not delete an instance if it was not launched by a NodeClaim", func() {
		m := mode()
		m.storeNoManagedBy()

		ExpectSingletonReconciled(ctx, InstanceGCController)
		_, err := cloudProvider.Get(ctx, m.getProviderID())
		Expect(err).NotTo(HaveOccurred())
	})

	It("should not delete an instance within the nodeClaim resolution window (5m)", func() {
		m := mode()
		m.storeInstanceNew()

		ExpectSingletonReconciled(ctx, InstanceGCController)
		_, err := cloudProvider.Get(ctx, m.getProviderID())
		Expect(err).NotTo(HaveOccurred())
	})

	It("should not delete the instance or node if it already has a nodeClaim that matches it", func() {
		m := mode()
		m.storeInstanceOld()

		nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
			Status: karpv1.NodeClaimStatus{
				ProviderID: m.getProviderID(),
			},
		})
		node := coretest.Node(coretest.NodeOptions{
			ProviderID: m.getProviderID(),
		})
		ExpectApplied(ctx, env.Client, nodeClaim, node)

		ExpectSingletonReconciled(ctx, InstanceGCController)
		_, err := cloudProvider.Get(ctx, m.getProviderID())
		Expect(err).ToNot(HaveOccurred())
		ExpectExists(ctx, env.Client, node)
	})

	It("should delete an instance along with the node if there is no NodeClaim owner (to quicken scheduling)", func() {
		m := mode()
		m.storeInstanceOld()

		node := coretest.Node(coretest.NodeOptions{
			ProviderID: m.getProviderID(),
		})
		ExpectApplied(ctx, env.Client, node)

		ExpectSingletonReconciled(ctx, InstanceGCController)
		_, err := cloudProvider.Get(ctx, m.getProviderID())
		Expect(err).To(HaveOccurred())
		Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

		ExpectNotFound(ctx, env.Client, node)
	})
}

var _ = Describe("Instance Garbage Collection", func() {
	var vm *armcompute.VirtualMachine
	var aksMachine *armcontainerservice.Machine
	var providerID string
	var err error

	var _ = Context("AKS machine instances", func() {
		BeforeEach(func() {
			// Enable AKS machines management for these tests
			testOptions = test.Options(test.OptionsFields{
				ManageExistingAKSMachines: lo.ToPtr(true),
			})
			ctx = options.ToContext(ctx, testOptions)

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

		// Shared GC tests (delete no owner, not launched, within window, matching claim, delete+node)
		runSharedInstanceGCTests(func() gcTestMode {
			return gcTestMode{
				storeInstanceOld: func() {
					aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
					azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
				},
				storeInstanceNew: func() {
					aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp()))
					azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
				},
				storeNoManagedBy: func() {
					aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10)))
					aksMachine.Properties.Tags = lo.OmitBy(aksMachine.Properties.Tags, func(key string, value *string) bool {
						return key == launchtemplate.NodePoolTagKey
					})
					azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
				},
				getProviderID: func() string { return providerID },
			}
		})

		// AKS-specific: malformed/missing timestamp tests
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
			// Enable AKS machines management for these tests
			testOptions = test.Options(test.OptionsFields{
				ManageExistingAKSMachines: lo.ToPtr(true),
			})
			ctx = options.ToContext(ctx, testOptions)

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

	var _ = Context("VM instances", func() {
		var _ = Context("Basic", func() {
			BeforeEach(func() {
				vm = test.VirtualMachine(test.VirtualMachineOptions{Name: "vm-a", NodepoolName: "default"})
				providerID = utils.VMResourceIDToProviderID(ctx, lo.FromPtr(vm.ID))
			})

			// Shared GC tests (delete no owner, not launched, within window, matching claim, delete+node)
			runSharedInstanceGCTests(func() gcTestMode {
				return gcTestMode{
					storeInstanceOld: func() {
						vm.Properties = &armcompute.VirtualMachineProperties{
							TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
						}
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
					},
					storeInstanceNew: func() {
						vm.Properties = &armcompute.VirtualMachineProperties{
							TimeCreated: lo.ToPtr(time.Now()),
						}
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
					},
					storeNoManagedBy: func() {
						vm.Properties = &armcompute.VirtualMachineProperties{
							TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
						}
						vm.Tags = lo.OmitBy(vm.Tags, func(key string, value *string) bool {
							return key == launchtemplate.NodePoolTagKey
						})
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
					},
					getProviderID: func() string { return providerID },
				}
			})
		})

		// VM-specific: bulk provisioning tests
		var _ = Context("Pod pressure", func() {
			BeforeEach(func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, prov, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
				vm, err = azureEnv.VMInstanceProvider.Get(ctx, vmName)
				Expect(err).To(BeNil())
				providerID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
			})

			It("should delete many instances if they all don't have NodeClaim owners", func() {
				// Generate 100 instances that have different vmIDs
				var ids []string
				var vmName string
				for i := 0; i < 100; i++ {
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, prov, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					if azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len() == 1 {
						vmName = azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
						vm, err = azureEnv.VMInstanceProvider.Get(ctx, vmName)
						Expect(err).To(BeNil())
						providerID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
						newVM := test.VirtualMachine(test.VirtualMachineOptions{
							Name:         vmName,
							NodepoolName: "default",
							Properties: &armcompute.VirtualMachineProperties{
								TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
							},
						})
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(newVM.ID), newVM)
						ids = append(ids, *vm.ID)
					}
				}
				ExpectSingletonReconciled(ctx, InstanceGCController)

				wg := sync.WaitGroup{}
				for _, id := range ids {
					wg.Add(1)
					go func(id string) {
						defer GinkgoRecover()
						defer wg.Done()

						_, err := cloudProvider.Get(ctx, utils.VMResourceIDToProviderID(ctx, id))
						Expect(err).To(HaveOccurred())
						Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
					}(id)
				}
				wg.Wait()
			})
			It("should not delete all instances if they all have NodeClaim owners", func() {
				// Generate 100 instances that have different instanceIDs
				var ids []string
				var nodeClaims []*karpv1.NodeClaim
				var vmName string
				for i := 0; i < 100; i++ {
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, prov, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					if azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len() == 1 {
						vmName = azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
						vm, err = azureEnv.VMInstanceProvider.Get(ctx, vmName)
						Expect(err).To(BeNil())
						providerID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
						newVM := test.VirtualMachine(test.VirtualMachineOptions{
							Name:         vmName,
							NodepoolName: "default",
							Properties: &armcompute.VirtualMachineProperties{
								TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
							},
						})
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(newVM.ID), newVM)
						nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
							Status: karpv1.NodeClaimStatus{
								ProviderID: utils.VMResourceIDToProviderID(ctx, *vm.ID),
							},
						})
						ids = append(ids, *vm.ID)
						ExpectApplied(ctx, env.Client, nodeClaim)
						nodeClaims = append(nodeClaims, nodeClaim)
					}
				}
				ExpectSingletonReconciled(ctx, InstanceGCController)

				wg := sync.WaitGroup{}
				for _, id := range ids {
					wg.Add(1)
					go func(id string) {
						defer GinkgoRecover()
						defer wg.Done()

						_, err := cloudProvider.Get(ctx, utils.VMResourceIDToProviderID(ctx, id))
						Expect(err).ToNot(HaveOccurred())
					}(id)
				}
				wg.Wait()

				for _, nodeClaim := range nodeClaims {
					ExpectExists(ctx, env.Client, nodeClaim)
				}
			})
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

	var _ = Context("VM instances", func() {
		It("should not delete a network interface if a nodeclaim exists for it", func() {
			// Create and apply a NodeClaim that references this NIC
			nodeClaim := coretest.NodeClaim()
			ExpectApplied(ctx, env.Client, nodeClaim)

			// Create a managed NIC
			nic := test.Interface(test.InterfaceOptions{
				Name:         instance.GenerateResourceName(nodeClaim.Name),
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)

			nicsBeforeGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsBeforeGC)).To(Equal(1))

			// Run garbage collection
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)

			// Verify NIC still exists after GC
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(1))
		})
		It("should delete a NIC if there is no associated VM", func() {
			nic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			nic2 := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic2.ID), *nic2)
			nicsBeforeGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsBeforeGC)).To(Equal(2))
			// add a nic to azure env, and call reconcile. It should show up in the list before reconcile
			// then it should not showup after
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(0))
		})
		It("should not delete a NIC if there is an associated VM", func() {
			managedNic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
			managedVM := test.VirtualMachine(test.VirtualMachineOptions{Name: lo.FromPtr(managedNic.Name), NodepoolName: nodePool.Name})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(managedVM.ID), *managedVM)
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			// We should still have a network interface here
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(1))

		})
		It("the vm gc controller should handle deletion of network interfaces if a nic is associated with a vm", func() {
			managedNic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
			managedVM := test.VirtualMachine(test.VirtualMachineOptions{
				Name:         lo.FromPtr(managedNic.Name),
				NodepoolName: nodePool.Name,
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 16)), // Needs to be older than the nodeclaim registration ttl
				},
			})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(managedVM.ID), *managedVM)
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			// We should still have a network interface here
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(1))

			ExpectSingletonReconciled(ctx, InstanceGCController)
			nicsAfterVMReconciliation, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterVMReconciliation)).To(Equal(0))

		})
	})
})
