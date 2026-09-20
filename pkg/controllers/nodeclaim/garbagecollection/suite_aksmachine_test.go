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
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	nodeclaimutils "github.com/Azure/karpenter-provider-azure/pkg/utils/nodeclaim"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/node/termination"
	"sigs.k8s.io/karpenter/pkg/controllers/node/termination/terminator"
	coregarbagecollection "sigs.k8s.io/karpenter/pkg/controllers/nodeclaim/garbagecollection"
	"sigs.k8s.io/karpenter/pkg/controllers/nodeclaim/lifecycle"
	"sigs.k8s.io/karpenter/pkg/events"
	"sigs.k8s.io/karpenter/pkg/state/nodepoolhealth"
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

		It("should delete an AKS machine if there is no NodeClaim owner", func() {
			// Launch happened 10m ago
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10))
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})

		It("should not delete an AKS machine if there is no NodeClaim owner, but was not launched by a NodeClaim", func() {
			// Remove the managed-by tag (this isn't launched by a NodeClaim)
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10))
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
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp())
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectSingletonReconciled(ctx, InstanceGCController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not delete the AKS machine or node if it already has a nodeClaim that matches it", func() {
			// Launch time was 10m ago
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10))
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
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10))
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

		Context("registered machines with missing backing VMs", func() {
			var nodeClaim *karpv1.NodeClaim
			var node *corev1.Node

			BeforeEach(func() {
				aksMachine.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
				aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Hour))
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
				nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
					Spec:   karpv1.NodeClaimSpec{NodeClassRef: nodePool.Spec.Template.Spec.NodeClassRef},
					Status: karpv1.NodeClaimStatus{ProviderID: providerID},
				})
				nodeClaim.Annotations = map[string]string{v1beta1.AnnotationAKSMachineResourceID: lo.FromPtr(aksMachine.ID)}
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				node = coretest.Node(coretest.NodeOptions{
					ProviderID:  providerID,
					ReadyStatus: corev1.ConditionUnknown,
				})
				node.Labels = nodeClaim.Labels
				ExpectApplied(ctx, env.Client, nodeClaim, node)
			})

			DescribeTable("should collect an evicted machine despite its registered NodeClaim", func(mode string, ready corev1.ConditionStatus) {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					ProvisionMode:             lo.ToPtr(mode),
					ManageExistingAKSMachines: lo.ToPtr(true),
				}))
				node.Status.Conditions[0].Status = ready
				ExpectApplied(ctx, env.Client, node)

				// The entire pool is unhealthy, but confirmed missing VMs are garbage collection, not node repair.
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				ExpectNotFound(ctx, env.Client, node)
				ExpectSingletonReconciled(ctx, coregarbagecollection.NewController(fakeClock, env.Client, cloudProvider))
				ExpectNotFound(ctx, env.Client, nodeClaim)
				ExpectSingletonReconciled(ctx, InstanceGCController)
			},
				Entry("machine API, unknown node", consts.ProvisionModeAKSMachineAPI, corev1.ConditionUnknown),
				Entry("machine API, not-ready node", consts.ProvisionModeAKSMachineAPI, corev1.ConditionFalse),
				Entry("batched machine API", consts.ProvisionModeAKSMachineAPIHeaderBatch, corev1.ConditionUnknown),
				Entry("existing machine in VM mode", consts.ProvisionModeAKSScriptless, corev1.ConditionUnknown),
			)

			It("should also collect an on-demand machine with a missing VM", func() {
				aksMachine.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular)
				azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				ExpectNotFound(ctx, env.Client, node)
			})

			It("should preserve a ready node without querying its VM", func() {
				node.Status.Conditions[0].Status = corev1.ConditionTrue
				ExpectApplied(ctx, env.Client, node)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectExists(ctx, env.Client, node)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(BeZero())
			})

			It("should preserve a machine whose NodeClaim is not registered", func() {
				nodeClaim.StatusConditions().SetUnknown(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, nodeClaim)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectExists(ctx, env.Client, node)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(BeZero())
			})

			It("should preserve an unhealthy node whose VM still exists", func() {
				vmName, err := nodeclaimutils.GetVMName(providerID)
				Expect(err).NotTo(HaveOccurred())
				vm := test.VirtualMachine(test.VirtualMachineOptions{Name: vmName, Tags: map[string]*string{}})
				azureEnv.VirtualMachinesAPI.Instances.Store(fake.MkVMID(options.FromContext(ctx).NodeResourceGroup, vmName), *vm)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectExists(ctx, env.Client, node)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(Equal(1))
			})

			DescribeTable("should propagate VM lookup errors without deleting", func(status int) {
				lookupErr := &azcore.ResponseError{StatusCode: status}
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(lookupErr)
				_, err := InstanceGCController.Reconcile(ctx)
				Expect(err).To(MatchError(ContainSubstring("getting azure VM")))
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeFalse())
				ExpectExists(ctx, env.Client, node)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
			},
				Entry("forbidden", http.StatusForbidden),
				Entry("throttled", http.StatusTooManyRequests),
				Entry("server unavailable", http.StatusServiceUnavailable),
			)

			It("should preserve ambiguous node mappings", func() {
				duplicate := coretest.Node(coretest.NodeOptions{ProviderID: providerID})
				ExpectApplied(ctx, env.Client, duplicate)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectExists(ctx, env.Client, node)
				ExpectExists(ctx, env.Client, duplicate)
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(BeZero())
			})

			It("should preserve ambiguous NodeClaim mappings", func() {
				duplicate := coretest.NodeClaim(karpv1.NodeClaim{Status: karpv1.NodeClaimStatus{ProviderID: providerID}})
				duplicate.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, duplicate)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectExists(ctx, env.Client, node)
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(BeZero())
			})

			It("should preserve a machine without a node readiness signal", func() {
				node.Status.Conditions = nil
				ExpectApplied(ctx, env.Client, node)
				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectExists(ctx, env.Client, node)
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(BeZero())
			})

			It("should preserve a machine without a matching node", func() {
				Expect(env.Client.Delete(ctx, node)).To(Succeed())
				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Calls()).To(BeZero())
			})

			It("should retain the node and retry when deleting the machine fails", func() {
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(
					&azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}, fake.MaxCalls(1))
				_, err := InstanceGCController.Reconcile(ctx)
				Expect(err).To(MatchError(ContainSubstring("failed to begin delete AKS machine")))
				ExpectExists(ctx, env.Client, node)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())

				ExpectSingletonReconciled(ctx, InstanceGCController)
				ExpectNotFound(ctx, env.Client, node)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
			})

			It("should let existing termination controllers finish without draining a deleted VM", func() {
				fakeClock.SetTime(time.Now())
				node.Finalizers = []string{karpv1.TerminationFinalizer}
				nodeClaim.Finalizers = []string{karpv1.TerminationFinalizer}
				pod := coretest.Pod()
				pod.Spec.NodeName = node.Name
				pod.Annotations = map[string]string{karpv1.DoNotDisruptAnnotationKey: "true"}
				ExpectApplied(ctx, env.Client, nodeClaim, node, pod)

				ExpectSingletonReconciled(ctx, InstanceGCController)
				node = ExpectExists(ctx, env.Client, node)
				Expect(node.DeletionTimestamp.IsZero()).To(BeFalse())
				Expect(node.Finalizers).To(ContainElement(karpv1.TerminationFinalizer))

				recorder := events.NewRecorder(&record.FakeRecorder{})
				queue := terminator.NewQueue(env.Client, recorder)
				terminationController := termination.NewController(fakeClock, env.Client, cloudProvider,
					terminator.NewTerminator(fakeClock, env.Client, queue, recorder), recorder)
				_, err := terminationController.Reconcile(ctx, node)
				Expect(err).NotTo(HaveOccurred())
				ExpectNotFound(ctx, env.Client, node)
				ExpectExists(ctx, env.Client, pod)

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				lifecycleController := lifecycle.NewController(fakeClock, env.Client, cloudProvider, recorder, nodepoolhealth.NewState(), nil)
				_, err = lifecycleController.Reconcile(ctx, nodeClaim)
				Expect(err).NotTo(HaveOccurred())
				ExpectNotFound(ctx, env.Client, nodeClaim)
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
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10))
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
			aksMachine.Properties.Status.CreationTimestamp = lo.ToPtr(instance.NewAKSMachineTimestamp().Add(-time.Minute * 10))
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
				Tags:         test.ManagedTagsAKSMachine(nodePool.Name, "some-nodeclaim"),
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
