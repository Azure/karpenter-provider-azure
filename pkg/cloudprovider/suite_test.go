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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	corestatus "github.com/awslabs/operatorpkg/status"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	"sigs.k8s.io/karpenter/pkg/metrics"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/Azure/skewer"
)

var ctx context.Context
var testOptions *options.Options
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var azureEnvNonZonal *test.Environment
var coreProvisioner *provisioning.Provisioner
var coreProvisionerNonZonal *provisioning.Provisioner
var cloudProvider *CloudProvider
var cloudProviderNonZonal *CloudProvider
var cluster *state.Cluster
var clusterNonZonal *state.Cluster
var fakeClock *clock.FakeClock
var recorder events.Recorder
var statusController *status.Controller

var nodePool *karpv1.NodePool
var nodeClass *v1beta1.AKSNodeClass
var nodeClaim *karpv1.NodeClaim

var fakeZone1 = utils.GetAKSZoneFromARMZone(fake.Region, "1")
var defaultTestSKU = &skewer.SKU{Name: lo.ToPtr("Standard_D2_v3"), Family: lo.ToPtr("standardD2v3Family")}

func ExpectLaunched(ctx context.Context, c client.Client, cloudProvider corecloudprovider.CloudProvider, provisioner *provisioning.Provisioner, pods ...*v1.Pod) {
	GinkgoHelper()
	// Persist objects
	for _, pod := range pods {
		ExpectApplied(ctx, c, pod)
	}
	results, err := provisioner.Schedule(ctx)
	Expect(err).ToNot(HaveOccurred())
	for _, m := range results.NewNodeClaims {
		var nodeClaimName string
		nodeClaimName, err = provisioner.Create(ctx, m, provisioning.WithReason(metrics.ProvisionedReason))
		Expect(err).ToNot(HaveOccurred())
		nodeClaim := &karpv1.NodeClaim{}
		Expect(c.Get(ctx, types.NamespacedName{Name: nodeClaimName}, nodeClaim)).To(Succeed())
		_, err = ExpectNodeClaimDeployedNoNode(ctx, c, cloudProvider, nodeClaim)
		Expect(err).ToNot(HaveOccurred())
	}
}

func TestCloudProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "cloudProvider/Azure")
}

var _ = BeforeSuite(func() {
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...), coretest.WithFieldIndexers(coretest.NodeProviderIDFieldIndexer(ctx)))
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx, stop = context.WithCancel(ctx)
	fakeClock = clock.NewFakeClock(time.Now())
	recorder = events.NewRecorder(&record.FakeRecorder{})
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	nodeClass = test.AKSNodeClass()
	nodePool = coretest.NodePool(karpv1.NodePool{
		Spec: karpv1.NodePoolSpec{
			Template: karpv1.NodeClaimTemplate{
				Spec: karpv1.NodeClaimTemplateSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
				},
			},
		},
	})

	nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
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

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

// Helper functions for NodeClaim validation
func validateNodeClaimCommon(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Basic validation
	Expect(nodeClaim).ToNot(BeNil())
	Expect(nodeClaim.Status.Capacity).ToNot(BeEmpty())

	// Status fields
	Expect(nodeClaim.Status.ProviderID).ToNot(BeEmpty())
	Expect(nodeClaim.Status.ImageID).ToNot(BeEmpty())

	// Common labels validation
	Expect(nodeClaim.Labels).To(HaveKey(karpv1.CapacityTypeLabelKey))
	Expect(nodeClaim.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
	if nodePool != nil {
		Expect(nodeClaim.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name))
	}
	Expect(nodeClaim.Labels).To(HaveKey(v1.LabelInstanceTypeStable))
	Expect(nodeClaim.Labels).To(HaveKey(v1.LabelArchStable))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUName))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUFamily))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUCPU))
	Expect(nodeClaim.Labels).To(HaveKey(v1beta1.LabelSKUMemory))

	// Zone validation (conditional)
	if nodeClaim.Labels[v1.LabelTopologyZone] != "" {
		Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(MatchRegexp(`^[a-z0-9-]+-[0-9]+$`))
	}

	// Capacity and Allocatable resources
	Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceCPU))
	Expect(nodeClaim.Status.Capacity).To(HaveKey(v1.ResourceMemory))
	Expect(nodeClaim.Status.Allocatable).To(HaveKey(v1.ResourceCPU))
	Expect(nodeClaim.Status.Allocatable).To(HaveKey(v1.ResourceMemory))

	// Lifecycle validation
	Expect(nodeClaim.CreationTimestamp).ToNot(BeZero())
	Expect(nodeClaim.DeletionTimestamp).To(BeNil())
}

func validateAKSMachineNodeClaim(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Common validations
	validateNodeClaimCommon(nodeClaim, nodePool)

	// AKS-specific annotations
	Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
	Expect(nodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]).ToNot(BeEmpty())
}

func validateVMNodeClaim(nodeClaim *karpv1.NodeClaim, nodePool *karpv1.NodePool) {
	// Common validations
	validateNodeClaimCommon(nodeClaim, nodePool)

	// VM-specific validation (should NOT have AKS machine annotation)
	Expect(nodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionModeAKSScriptless", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSScriptless),
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
		})

		AfterEach(func() {
			cluster.Reset()
			azureEnv.Reset()
		})

		It("should list nodeclaim created by the CloudProvider", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

			nodeClaims, _ := cloudProvider.List(ctx)
			Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(Equal(1)) // Expect to be called in case of existing AKS machines
			Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(Equal(1))
			queryRequest := azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Pop().Query
			Expect(*queryRequest.Query).To(Equal(instance.GetVMListQueryBuilder(azureEnv.AzureResourceGraphAPI.ResourceGroup).String()))
			Expect(nodeClaims).To(HaveLen(1))
			validateVMNodeClaim(nodeClaims[0], nodePool)
			resp, _ := azureEnv.VirtualMachinesAPI.Get(ctx, azureEnv.AzureResourceGraphAPI.ResourceGroup, nodeClaims[0].Name, nil)
			Expect(resp.VirtualMachine).ToNot(BeNil())
		})
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
		It("should not call writes to AKS Machine API", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
		})

		Context("AKS Machines Pool Management", func() {
			It("should handle AKS machines pool not found on each CloudProvider operation", func() {
				// First create a successful VM
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// cloudprovider.List should return vm nodeclaim
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))
				validateVMNodeClaim(nodeClaims[0], nodePool)

				// cloudprovider.Delete should be fine also
				err = cloudProvider.Delete(ctx, nodeClaims[0])
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

	Context("ProvisionModeAKSMachineAPI", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				UseSIG:        lo.ToPtr(true),
			})

			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI)
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider)
			cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)

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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			Expect(createdNodeClaim.Name).To(ContainSubstring(createInput.AKSMachineName))
			validateAKSMachineNodeClaim(createdNodeClaim, nodePool)

			// Get should return the created nodeClaim
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			nodeClaim, err := cloudProvider.Get(ctx, createdNodeClaim.Status.ProviderID)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

			//// The returned nodeClaim should be correct
			Expect(nodeClaim.Name).To(ContainSubstring(createInput.AKSMachineName))
			validateAKSMachineNodeClaim(nodeClaim, nodePool)

			// Delete
			azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
			Expect(cloudProvider.Delete(ctx, nodeClaim)).To(Succeed())
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
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1)) // Should be bothered as AKS machine is not found, so suspect this to be a VM
			Expect(nodeClaim).To(BeNil())
		})

		// XPMT: TODO: check API: simulate all of these and see if behavior matches (logs could be sufficient)
		Context("Unexpected API Failures", func() {
			It("should handle AKS machine create failures - unrecognized error during sync/initial", func() {
				// Set up error to occur immediately during BeginCreateOrUpdate call
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)

				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

				// Verify the cleanup was attempted
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

				// Clear the error for cleanup
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(nil)

				// Verify the pod is now schedulable
				pod2 := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod2)
				ExpectScheduled(ctx, env.Client, pod2)
			})

			It("should handle AKS machine create failures - unrecognized error during async/LRO", func() {
				// Set up error to occur during LRO polling (async failure)
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAny()

				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				// Verify the create API was called but failed
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))

				// Verify the cleanup was attempted
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))

				// Clear the error for cleanup
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil

				// Verify the pod is now schedulable
				pod2 := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod2)
				ExpectScheduled(ctx, env.Client, pod2)
			})

			It("should handle AKS machine get failures - unrecognized error", func() {
				// First create a successful AKS machine
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Get the created nodeclaim
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))
				validateAKSMachineNodeClaim(nodeClaims[0], nodePool)

				// Set up Get to fail
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

				// Attempt to get the nodeclaim - should fail
				nodeClaim, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
				Expect(err).To(HaveOccurred())
				Expect(nodeClaim).To(BeNil())
				// Verify the get API was called
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

				// Clear the error for cleanup
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)
			})

			It("should handle AKS machine delete failures - unrecognized error during sync/initial", func() {
				// First create a successful AKS machine
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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

			// XPMT: TODO: check API: list don't generally return errors
			It("should handle AKS machine list failures - unrecognized error", func() {
				// Set up error to occur during the NextPage call
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(fake.AKSMachineAPIErrorAny)

				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Verify the list API was called but failed
				azureEnv.AKSAgentPoolsAPI.AgentPoolGetBehavior.CalledWithInput.Reset()
				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).To(HaveOccurred())
				Expect(nodeClaims).To(BeEmpty())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolGetBehavior.CalledWithInput.Len()).To(Equal(1)) // Check after seeing error

				// Clear the error for cleanup
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(nil)

				// Verify the pod is now schedulable
				pod2 := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod2)
				ExpectScheduled(ctx, env.Client, pod2)
			})
		})

		Context("Operation Conflicts/Races", func() {
			It("should handle AKS machine get/delete failures - not found/already deleted externally", func() {
				// First create a successful AKS machine
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				nodeClaim, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
				Expect(err).To(HaveOccurred())
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(nodeClaim).To(BeNil())

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
									Values:   []string{utils.GetAKSZoneFromARMZone(fake.Region, "1")},
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
				createdFirstNodeClaim, err := cloudProvider.Create(ctx, firstNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(createdFirstNodeClaim).ToNot(BeNil())
				validateAKSMachineNodeClaim(createdFirstNodeClaim, nodePool)

				// Create a conflicted nodeclaim with same configuration
				conflictedNodeClaim := firstNodeClaim.DeepCopy()

				// Call cloudProvider.Create directly with the unconflicted nodeclaim to trigger get
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
				nodeClaim, err = cloudProvider.Create(ctx, conflictedNodeClaim)
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
				existingMachine, ok := azureEnv.SharedStores.AKSMachines.Load(machineID)
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
				Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(utils.GetAKSZoneFromARMZone(fake.Region, "1")))
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
									Values:   []string{utils.GetAKSZoneFromARMZone(fake.Region, "1")},
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
				createdFirstNodeClaim, err := cloudProvider.Create(ctx, firstNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(createdFirstNodeClaim).ToNot(BeNil())
				validateAKSMachineNodeClaim(createdFirstNodeClaim, nodePool)

				// Create a conflicted nodeclaim with same configuration
				conflictedNodeClaim := firstNodeClaim.DeepCopy()

				// Simulate Get being faulty (or the previous machine comes into exist between get and create)
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorFromAKSMachineNotFound)

				// Call cloudProvider.Create directly with the unconflicted nodeclaim to trigger empty create
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				nodeClaim, err = cloudProvider.Create(ctx, conflictedNodeClaim)
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
				Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(utils.GetAKSZoneFromARMZone(fake.Region, "1")))
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
									Values:   []string{utils.GetAKSZoneFromARMZone(fake.Region, "1")},
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
				createdFirstNodeClaim, err := cloudProvider.Create(ctx, firstNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(createdFirstNodeClaim).ToNot(BeNil())
				validateAKSMachineNodeClaim(createdFirstNodeClaim, nodePool)

				// Create a conflicted nodeclaim with different immutable configuration (zone/SKU)
				conflictedNodeClaim := firstNodeClaim.DeepCopy()
				// Change zone to create immutable configuration conflict
				conflictedNodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
					{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelTopologyZone,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{utils.GetAKSZoneFromARMZone(fake.Region, "2")}, // Different zone
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
				_, err = cloudProvider.Create(ctx, conflictedNodeClaim)
				Expect(err).To(HaveOccurred())

				// Verify cleanup was attempted after the conflict
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))

				// Clear the error
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)

				// Should succeed now that the conflicted node is gone from the cleanup
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				nodeClaim, err := cloudProvider.Create(ctx, conflictedNodeClaim)
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
				Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal("Standard_D2_v5"))
				Expect(aksMachine.Zones).To(HaveLen(1))
				Expect(*aksMachine.Zones[0]).To(Equal("2"))

				// Validate the returned nodeClaim has correct configuration
				validateAKSMachineNodeClaim(nodeClaim, nodePool)
				Expect(nodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(utils.GetAKSZoneFromARMZone(fake.Region, "2")))
				Expect(nodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v5"))
			})
		})

		// Mostly ported from VM test: "VM Creation Failures"
		Context("Create - Creation Failures", func() {
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
				zone, err := instance.GetAKSZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
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
				initialZone, err := instance.GetAKSZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
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
				zone, err := instance.GetAKSZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
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
				zone, err := instance.GetAKSZoneFromAKSMachine(&aksMachine, fake.Region)
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
				nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
				claim, err := cloudProvider.Create(ctx, nodeClaim)
				Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
				Expect(claim).To(BeNil())
			})
		})

		// Ported from VM test: Context "additional-tags"
		Context("Create - Additional Tags", func() {
			It("should add additional tags to the AKS machine", func() {
				// Set up test context with additional tags
				aksTestOptions := test.Options(test.OptionsFields{
					ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
					UseSIG:        lo.ToPtr(true),
					AdditionalTags: map[string]string{
						"karpenter.azure.com/test-tag": "test-value",
					},
				})
				aksCtx := coreoptions.ToContext(ctx, coretest.Options())
				aksCtx = options.ToContext(aksCtx, aksTestOptions)

				aksAzureEnv := test.NewEnvironment(aksCtx, env)
				test.ApplyDefaultStatus(nodeClass, env, aksTestOptions.UseSIG)
				aksCloudProvider := New(aksAzureEnv.InstanceTypesProvider, aksAzureEnv.VMInstanceProvider, aksAzureEnv.AKSMachineProvider, recorder, env.Client, aksAzureEnv.ImageProvider)
				aksCluster := state.NewCluster(fakeClock, env.Client, aksCloudProvider)
				aksProv := provisioning.NewProvisioner(env.Client, recorder, aksCloudProvider, aksCluster, fakeClock)

				ExpectApplied(aksCtx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(aksCtx, env.Client, aksCluster, aksCloudProvider, aksProv, pod)
				ExpectScheduled(aksCtx, env.Client, pod)

				// Verify AKS machine was created with expected tags
				Expect(aksAzureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := aksAzureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := input.AKSMachine
				Expect(aksMachine).ToNot(BeNil())
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_test-tag"))
				Expect(*aksMachine.Properties.Tags["karpenter.azure.com_test-tag"]).To(Equal("test-value"))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
				Expect(*aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal("test-cluster"))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.sh_nodepool"))
				Expect(*aksMachine.Properties.Tags["karpenter.sh_nodepool"]).To(Equal(nodePool.Name))
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine"))
				Expect(*aksMachine.Properties.Tags["karpenter.azure.com_aksmachine"]).To(Equal("true"))

				// Clean up
				aksCluster.Reset()
				aksAzureEnv.Reset()
			})
		})

		// Mostly ported from VM test: Context "Ephemeral Disk"
		// Note: AKS Machine API has simpler disk configuration compared to VM API
		// - VMs control detailed StorageProfile, DiffDiskSettings, Placement (NVMe/Cache)
		// - AKS machines use OSDiskType (Managed/Ephemeral) and OSDiskSizeGB
		// - AKS machines automatically handles placement decisions (NVMe vs Cache disk)
		Context("Create - Ephemeral Disk", func() {
			// Ported from VM test: "should use ephemeral disk if supported, and has space of at least 128GB by default"
			It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
				// Select a SKU that supports ephemeral disks with sufficient space
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D64s_v3"}, // Has large cache disk space
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Verify AKS machine uses ephemeral disk
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
			})

			// Ported from VM test: "should fail to provision if ephemeral disk ask for is too large"
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

			})

			// Ported from VM test: should select an ephemeral disk if LabelSKUStorageEphemeralOSMaxSize is set and os disk size fits
			It("should select an ephemeral disk if LabelSKUStorageEphemeralOSMaxSize is set and os disk size fits", func() {
				// Select instances that support ephemeral disks
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Should select a SKU with ephemeral capability
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				// Should use ephemeral since we required sufficient ephemeral storage
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(30)))
			})

			// Ported from VM test: "should use ephemeral disk if supported, and set disk size to OSDiskSizeGB from node class"
			It("should use ephemeral disk if supported, and set disk size to OSDiskSizeGB from node class", func() {
				// Configure specific OS disk size in NodeClass
				nodeClass.Spec.OSDiskSizeGB = lo.ToPtr(int32(256))

				// Select an instance type that supports the disk size
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Verify AKS machine was created with correct OS disk size
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(256)))
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeEphemeral))
			})

			// Ported from VM test: "should not use ephemeral disk if ephemeral is supported, but we don't have enough space"
			It("should not use ephemeral disk if ephemeral is supported, but we don't have enough space", func() {
				// Select Standard_D2s_v3 which supports ephemeral but has limited space
				// Standard_D2s_V3 has 53GB Of CacheDisk space and 16GB of Temp Disk Space.
				// With our rule of 128GB being the minimum OSDiskSize, this should fall back to managed disk
				nodePool.Spec.Template.Spec.Requirements = append(nodePool.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2s_v3"},
					},
				})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				// Should fall back to managed disk due to insufficient ephemeral space
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Properties.OperatingSystem).ToNot(BeNil())
				Expect(aksMachine.Properties.OperatingSystem.OSDiskType).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskType).To(Equal(armcontainerservice.OSDiskTypeManaged))
				Expect(aksMachine.Properties.OperatingSystem.OSDiskSizeGB).ToNot(BeNil())
				Expect(*aksMachine.Properties.OperatingSystem.OSDiskSizeGB).To(Equal(int32(128))) // Default size
			})
		})

		// Inspired by VM test: "Nodepool with KubeletConfig", but with larger scope
		Context("Create - Additional Configurations", func() {
			It("should handle configured NodeClass", func() {
				// Configure comprehensive NodeClass settings
				nodeClass.Spec.Kubelet = &v1beta1.KubeletConfiguration{
					CPUManagerPolicy:            "static",
					CPUCFSQuota:                 lo.ToPtr(true),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(85)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(80)),
				}
				nodeClass.Spec.ImageFamily = lo.ToPtr(v1beta1.Ubuntu2204ImageFamily)
				nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet")
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

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

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
				Expect(*aksMachine.Properties.Kubernetes.ArtifactStreamingProfile.Enabled).To(BeTrue())

				// Verify subnet configuration (AKS machine should use the specified subnet)
				Expect(aksMachine.Properties.Network).ToNot(BeNil())
				Expect(aksMachine.Properties.Network.VnetSubnetID).ToNot(BeNil())
				Expect(*aksMachine.Properties.Network.VnetSubnetID).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"))

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
				Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine"))
				Expect(aksMachine.Properties.Tags["karpenter.azure.com_aksmachine"]).To(Equal(lo.ToPtr("true")))

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

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim) // XPMT: ExpectApplied on nodeClaim needs cloudProvider.Create test rather than pending pods, otherwise it would be a different NodeCLaim?
				_, err := cloudProvider.Create(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())

				// Verify machine was created with correct taints
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				input := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				machine := input.AKSMachine

				// Check that taints are configured
				Expect(machine.Properties.Kubernetes.NodeTaints).To(ContainElement(lo.ToPtr("test-taint=test-value:NoSchedule")))
				Expect(machine.Properties.Kubernetes.NodeInitializationTaints).To(ContainElement(lo.ToPtr("startup-taint=startup-value:NoExecute")))
			})

			It("should not allow the user to override Karpenter-managed tags", func() {
				nodeClass.Spec.Tags = map[string]string{
					"karpenter.azure.com/cluster": "my-override-cluster",
					"karpenter.sh/nodepool":       "my-override-nodepool",
				}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass) // XPMT: this has to be here when NodeClass changes?
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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

		// Ported from VM test: Unavailable Offerings"
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
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorZoneAllocationFailed("1")

				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{"Standard_D2_v2"},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				// ExpectLaunched(ctx, env.Client, cloudProvider, coreProvisioner, pod) // XPMT: TODO: This assumes error is async (which is right, actually). However, our tests currently show failed result during first get. Need to fix test to show result during polling instead. And maybe adopt this ExpectLaunched widely?
				ExpectNotScheduled(ctx, env.Client, pod)

				// Eventually(func() []*karpv1.NodeClaim { return ExpectNodeClaims(ctx, env.Client) }).To(HaveLen(0)) // This too; if it fails on sync then we don't delete nodeclaim

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
				failedZone, err := instance.GetAKSZoneFromAKSMachine(&machineInput.AKSMachine, fake.Region)
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
						ExpectUnavailable(azureEnv, sku, utils.GetAKSZoneFromARMZone(fake.Region, zoneID), capacityType)
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

		// Mostly ported from VM test: "Provider list"
		Context("Create - Provider list", func() {
			// Ported from VM test: "should support individual instance type labels"
			It("should support individual instance type labels", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				nodeSelector := map[string]string{
					// Well known
					v1.LabelTopologyRegion:      fake.Region,
					karpv1.NodePoolLabelKey:     nodePool.Name,
					v1.LabelTopologyZone:        fakeZone1,
					v1.LabelInstanceTypeStable:  "Standard_NC24ads_A100_v4",
					v1.LabelOSStable:            "linux",
					v1.LabelArchStable:          "amd64",
					karpv1.CapacityTypeLabelKey: "on-demand",
					// Well Known to AKS
					v1beta1.LabelSKUName:                      "Standard_NC24ads_A100_v4",
					v1beta1.LabelSKUFamily:                    "N",
					v1beta1.LabelSKUVersion:                   "4",
					v1beta1.LabelSKUStorageEphemeralOSMaxSize: "429",
					v1beta1.LabelSKUAcceleratedNetworking:     "true",
					v1beta1.LabelSKUStoragePremiumCapable:     "true",
					v1beta1.LabelSKUGPUName:                   "A100",
					v1beta1.LabelSKUGPUManufacturer:           "nvidia",
					v1beta1.LabelSKUGPUCount:                  "1",
					v1beta1.LabelSKUCPU:                       "24",
					v1beta1.LabelSKUMemory:                    "8192",
					// Deprecated Labels
					v1.LabelFailureDomainBetaRegion:    fake.Region,
					v1.LabelFailureDomainBetaZone:      fakeZone1,
					"beta.kubernetes.io/arch":          "amd64",
					"beta.kubernetes.io/os":            "linux",
					v1.LabelInstanceType:               "Standard_NC24ads_A100_v4",
					"topology.disk.csi.azure.com/zone": fakeZone1,
					v1.LabelWindowsBuild:               "window",
					// Cluster Label
					v1beta1.AKSLabelCluster: "test-cluster",
				}

				// Ensure that we're exercising all well known labels
				Expect(lo.Keys(nodeSelector)).To(ContainElements(append(karpv1.WellKnownLabels.UnsortedList(), lo.Keys(karpv1.NormalizedLabels)...)))

				var pods []*v1.Pod
				for key, value := range nodeSelector {
					pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{key: value}}))
				}
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pods...)
				for _, pod := range pods {
					ExpectScheduled(ctx, env.Client, pod)
				}
			})
		})

		// Mostly ported from VM test: "ImageReference" and "ImageProvider + Image Family"
		// Note: AKS Machine API does not support Community Image Gallery (CIG)
		Context("Create - ImageReference and ImageProvider + Image Family", func() {

			// Ported from VM test: "should use shared image gallery images when options are set to UseSIG"
			It("should use shared image gallery images", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{instanceType},
						}})

					ExpectApplied(ctx, env.Client, nodePool, nodeClass)
					ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
					pod := coretest.UnschedulablePod(coretest.PodOptions{})
					ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				// XPMT: TODO: The below doesn't work as the image definition cannot be defined outside due to "no assertion outside". And earlier declaration doesn't help.
				// Somehow it did not happen with instancetype/suite_test.go...
				// Entry("Gen2 instance type with AzureLinux image family", "Standard_D2_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ImageDefinition),
				// Entry("Gen1 instance type with AzureLinux image family", "Standard_D2_v3", v1beta1.AzureLinuxImageFamily, azureLinuxGen1ImageDefinition),
				// Entry("ARM instance type with AzureLinux image family", "Standard_D16plds_v5", v1beta1.AzureLinuxImageFamily, azureLinuxGen2ArmImageDefinition),
			)

			It("should select the right Shared Image Gallery NodeImageVersion for a given instance type, Gen2 instance type with AzureLinux image family", func() {
				instanceType := "Standard_D2_v5"
				imageFamily := v1beta1.AzureLinuxImageFamily
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ImageDefinition, imagefamily.AzureLinuxGen2ImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass) // XPMT: Claude somehow removed this without telling when porting. Dammit.
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

				// NodeImageVersion should contain the expected image definition
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			},
			)

			It("should select the right Shared Image Gallery NodeImageVersion for a given instance type, Gen1 instance type with AzureLinux image family", func() {
				instanceType := "Standard_D2_v3"
				imageFamily := v1beta1.AzureLinuxImageFamily
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen1ImageDefinition, imagefamily.AzureLinuxGen1ImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties.NodeImageVersion).ToNot(BeNil())

				// NodeImageVersion should contain the expected image definition
				nodeImageVersion := lo.FromPtr(aksMachine.Properties.NodeImageVersion)
				Expect(nodeImageVersion).To(ContainSubstring(expectedImageDefinition))
			},
			)

			It("should select the right Shared Image Gallery NodeImageVersion for a given instance type, ARM instance type with AzureLinux image family", func() {
				instanceType := "Standard_D16plds_v5"
				imageFamily := v1beta1.AzureLinuxImageFamily
				kubernetesVersion := lo.Must(env.KubernetesInterface.Discovery().ServerVersion()).String()
				expectUseAzureLinux3 := imagefamily.UseAzureLinux3(kubernetesVersion)
				expectedImageDefinition := lo.Ternary(expectUseAzureLinux3, imagefamily.AzureLinux3Gen2ArmImageDefinition, imagefamily.AzureLinuxGen2ArmImageDefinition)

				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			},
			)
		})

		// Ported from VM test: "GPU Workloads + Nodes"
		Context("Create - GPU Workloads + Nodes", func() {
			// Ported from VM test: "should schedule non-GPU pod onto the cheapest non-GPU capable node"
			It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				Expect(utils.IsNvidiaEnabledSKU(lo.FromPtr(aksMachine.Properties.Hardware.VMSize))).To(BeFalse())

				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "0"))
			})

			// Ported from VM test: "should schedule GPU pod on GPU capable node"
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

				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node := ExpectScheduled(ctx, env.Client, pod)

				// the following checks assume Standard_NC16as_T4_v3 (surprisingly the cheapest GPU in the test set), so test the assumption
				Expect(node.Labels).To(HaveKeyWithValue("node.kubernetes.io/instance-type", "Standard_NC16as_T4_v3"))

				// Verify AKS machine GPU selection
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				vmSize := lo.FromPtr(aksMachine.Properties.Hardware.VMSize)
				Expect(utils.IsNvidiaEnabledSKU(vmSize)).To(BeTrue())

				// Verify that the node the pod was scheduled on has GPU resource and labels set
				Expect(node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-name", "T4"))
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-manufacturer", v1beta1.ManufacturerNvidia))
				Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "1"))
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
		Context("Create - CloudProvider Error Cases", func() {
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
				nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

				ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
				claim, err := cloudProvider.Create(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("resolving NodeClass readiness, NodeClass is in Ready=Unknown"))
			})

			// Ported from VM test: "should return error when instance type resolution fails"
			It("should return error when instance type resolution fails", func() {
				// Create and set up the status controller
				statusController := status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI)

				// Set NodeClass to Ready
				nodeClass.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				// Reconcile the NodeClass to ensure status is updated
				ExpectObjectReconciled(ctx, env.Client, statusController, nodeClass)

				azureEnv.SKUsAPI.Error = fmt.Errorf("failed to list SKUs")

				nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

				claim, err := cloudProvider.Create(ctx, nodeClaim)
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
				nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

				claim, err := cloudProvider.Create(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
				Expect(claim).To(BeNil())
				Expect(err.Error()).To(ContainSubstring("creating AKS machine failed"))
			})
		})

		Context("AKS Machine Drift Detection", func() {
			var nodeClaim *karpv1.NodeClaim
			var node *v1.Node
			var createInput *fake.AKSMachineCreateOrUpdateInput

			BeforeEach(func() {
				instanceType := "Standard_D2_v2"
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
				})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				node = ExpectScheduled(ctx, env.Client, pod)
				// KubeletVersion must be applied to the node to satisfy k8s drift
				node.Status.NodeInfo.KubeletVersion = "v" + nodeClass.Status.KubernetesVersion
				node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2" // random UUID

				ExpectApplied(ctx, env.Client, node)
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput = azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()

				nodeClaims, err := cloudProvider.List(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(nodeClaims).To(HaveLen(1))

				nodeClaim = nodeClaims[0]
				nodeClaim.Status.NodeName = node.Name // Normally core would do this.
				nodeClaim.Spec.NodeClassRef = &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				}
			})

			It("should not fail if nodeClass does not exist", func() {
				ExpectDeleted(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should not fail if nodePool does not exist", func() {
				ExpectDeleted(ctx, env.Client, nodePool)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should not return drifted if the NodeClaim is valid", func() {
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should error drift if NodeClaim doesn't have provider id", func() {
				nodeClaim.Status = karpv1.NodeClaimStatus{}
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			Context("Node Image Drift", func() {
				It("should trigger drift when DriftAction field is available", func() {
					// Find the AKS machine that was created during BeforeEach
					aksMachineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, createInput.AKSMachineName)

					// Get the existing machine from the fake store
					existingMachine, ok := azureEnv.SharedStores.AKSMachines.Load(aksMachineID)
					Expect(ok).To(BeTrue(), "AKS machine should exist in fake store")

					aksMachine := existingMachine.(armcontainerservice.Machine)

					// Set DriftAction to "Recreate" to trigger drift
					if aksMachine.Properties == nil {
						aksMachine.Properties = &armcontainerservice.MachineProperties{}
					}
					if aksMachine.Properties.Status == nil {
						aksMachine.Properties.Status = &armcontainerservice.MachineStatus{}
					}
					aksMachine.Properties.Status.DriftAction = lo.ToPtr(armcontainerservice.DriftActionRecreate)
					aksMachine.Properties.Status.DriftReason = lo.ToPtr("ClusterConfigurationChanged")

					// Update the machine in the fake store
					azureEnv.SharedStores.AKSMachines.Store(aksMachineID, aksMachine)

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ClusterConfigDrift))
				})
			})

			Context("Node Image Drift", func() {
				It("should succeed with no drift when nothing changes", func() {
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should succeed with no drift when ConditionTypeImagesReady is not true", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNoLongerReady", "test when images aren't ready")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				// Note: this case shouldn't be able to happen in practice since if Images is empty ConditionTypeImagesReady should be false.
				It("should error when Images are empty", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.Status.Images = []v1beta1.NodeImage{}
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).To(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should trigger drift when the image version changes", func() {
					test.ApplyCIGImagesWithVersion(nodeClass, "202503.02.0")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(ImageDrift))
				})
			})

			Context("Kubernetes Version", func() {
				It("should succeed with no drift when nothing changes", func() {
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("should succeed with no drift when KubernetesVersionReady is not true", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "K8sVersionNoLongerReady", "test when k8s isn't ready")
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
				It("shouldn't error or be drifted when KubernetesVersion is empty", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)
					nodeClass.Status.KubernetesVersion = ""
					ExpectApplied(ctx, env.Client, nodeClass)
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when NodeName is missing", func() {
					nodeClaim.Status.NodeName = ""
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when node is not found", func() {
					nodeClaim.Status.NodeName = "NodeWhoDoesNotExist"
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))
				})

				It("shouldn't error or be drifted when node is deleting", func() {
					node = ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
					node.Finalizers = append(node.Finalizers, test.TestingFinalizer)
					ExpectApplied(ctx, env.Client, node)
					Expect(env.Client.Delete(ctx, node)).ToNot(HaveOccurred())
					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(NoDrift))

					// cleanup
					node = ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
					deepCopy := node.DeepCopy()
					node.Finalizers = lo.Reject(node.Finalizers, func(finalizer string, _ int) bool {
						return finalizer == test.TestingFinalizer
					})
					Expect(env.Client.Patch(ctx, node, client.StrategicMergeFrom(deepCopy))).NotTo(HaveOccurred())
					ExpectDeleted(ctx, env.Client, node)
				})

				It("should succeed with drift true when KubernetesVersion is new", func() {
					nodeClass = ExpectExists(ctx, env.Client, nodeClass)

					semverCurrentK8sVersion := lo.Must(semver.ParseTolerant(nodeClass.Status.KubernetesVersion))
					semverCurrentK8sVersion.Minor = semverCurrentK8sVersion.Minor + 1
					nodeClass.Status.KubernetesVersion = semverCurrentK8sVersion.String()

					ExpectApplied(ctx, env.Client, nodeClass)

					drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
					Expect(err).ToNot(HaveOccurred())
					Expect(drifted).To(Equal(K8sVersionDrift))
				})
			})
		})
	})

	// TODO (chmcbrid): split Drift tests into their own test file drift_test.go
	Context("ProvisionModeAKSScriptless - Drift", func() {
		var nodeClaim *karpv1.NodeClaim
		var pod *v1.Pod
		var node *v1.Node

		BeforeEach(func() {
			// Set up VM provisioning mode environment for drift testing
			testOptions = test.Options()
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider)
			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)

			instanceType := "Standard_D2_v2"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod = coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: instanceType},
			})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node = ExpectScheduled(ctx, env.Client, pod)
			// KubeletVersion must be applied to the node to satisfy k8s drift
			node.Status.NodeInfo.KubeletVersion = "v" + nodeClass.Status.KubernetesVersion
			node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "61f71907-753f-4802-a901-47361c3664f2" // random UUID
			// Context must have same kubelet client id
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
				KubeletIdentityClientID: lo.ToPtr(node.Labels[v1beta1.AKSLabelKubeletIdentityClientID]),
			}))

			ExpectApplied(ctx, env.Client, node)
			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			input := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			rg := input.ResourceGroupName
			vmName := input.VMName
			// Corresponding NodeClaim
			nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
				Status: karpv1.NodeClaimStatus{
					NodeName: node.Name,
					// TODO (charliedmcb): switch back to use MkVMID, and update the test subscription usage to all use the same sub const 12345678-1234-1234-1234-123456789012
					//     We currently need this work around for the List nodes call to work in Drift, since the VM ID is overridden here (which uses the sub id in the instance provider):
					//     https://github.com/Azure/karpenter-provider-azure/blob/84e449787ec72268efb0c7af81ec87a6b3ee95fa/pkg/providers/instance/instance.go#L604
					//     which has the sub const 12345678-1234-1234-1234-123456789012 passed in here:
					//     https://github.com/Azure/karpenter-provider-azure/blob/84e449787ec72268efb0c7af81ec87a6b3ee95fa/pkg/test/environment.go#L152
					ProviderID: utils.VMResourceIDToProviderID(ctx, fmt.Sprintf("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", rg, vmName)),
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						karpv1.NodePoolLabelKey:    nodePool.Name,
						v1.LabelInstanceTypeStable: instanceType,
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

		It("should not fail if nodeClass does not exist", func() {
			ExpectDeleted(ctx, env.Client, nodeClass)
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})

		It("should not fail if nodePool does not exist", func() {
			ExpectDeleted(ctx, env.Client, nodePool)
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})

		It("should not return drifted if the NodeClaim is valid", func() {
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})

		It("should error drift if NodeClaim doesn't have provider id", func() {
			nodeClaim.Status = karpv1.NodeClaimStatus{}
			drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(drifted).To(BeEmpty())
		})

		Context("Node Image Drift", func() {
			It("should succeed with no drift when nothing changes", func() {
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("should succeed with no drift when ConditionTypeImagesReady is not true", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeImagesReady, "ImagesNoLongerReady", "test when images aren't ready")
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			// Note: this case shouldn't be able to happen in practice since if Images is empty ConditionTypeImagesReady should be false.
			It("should error when Images are empty", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)
				nodeClass.Status.Images = []v1beta1.NodeImage{}
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).To(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("should trigger drift when the image gallery changes to SIG", func() {
				test.ApplySIGImages(nodeClass)
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(ImageDrift))
			})

			It("should trigger drift when the image version changes", func() {
				test.ApplyCIGImagesWithVersion(nodeClass, "202503.02.0")
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(ImageDrift))
			})
		})

		Context("Kubernetes Version", func() {
			It("should succeed with no drift when nothing changes", func() {
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("should succeed with no drift when KubernetesVersionReady is not true", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)
				nodeClass.StatusConditions().SetFalse(v1beta1.ConditionTypeKubernetesVersionReady, "K8sVersionNoLongerReady", "test when k8s isn't ready")
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			// TODO (charliedmcb): I'm wondering if we actually want to have these soft-error cases switch to return an error if no-drift condition was found.
			It("shouldn't error or be drifted when KubernetesVersion is empty", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)
				nodeClass.Status.KubernetesVersion = ""
				ExpectApplied(ctx, env.Client, nodeClass)
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("shouldn't error or be drifted when NodeName is missing", func() {
				nodeClaim.Status.NodeName = ""
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("shouldn't error or be drifted when node is not found", func() {
				nodeClaim.Status.NodeName = "NodeWhoDoesNotExist"
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))
			})

			It("shouldn't error or be drifted when node is deleting", func() {
				node = ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
				node.Finalizers = append(node.Finalizers, test.TestingFinalizer)
				ExpectApplied(ctx, env.Client, node)
				Expect(env.Client.Delete(ctx, node)).ToNot(HaveOccurred())
				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(NoDrift))

				// cleanup
				node = ExpectNodeExists(ctx, env.Client, nodeClaim.Status.NodeName)
				deepCopy := node.DeepCopy()
				node.Finalizers = lo.Reject(node.Finalizers, func(finalizer string, _ int) bool {
					return finalizer == test.TestingFinalizer
				})
				Expect(env.Client.Patch(ctx, node, client.StrategicMergeFrom(deepCopy))).NotTo(HaveOccurred())
				ExpectDeleted(ctx, env.Client, node)
			})

			It("should succeed with drift true when KubernetesVersion is new", func() {
				nodeClass = ExpectExists(ctx, env.Client, nodeClass)

				semverCurrentK8sVersion := lo.Must(semver.ParseTolerant(nodeClass.Status.KubernetesVersion))
				semverCurrentK8sVersion.Minor = semverCurrentK8sVersion.Minor + 1
				nodeClass.Status.KubernetesVersion = semverCurrentK8sVersion.String()

				ExpectApplied(ctx, env.Client, nodeClass)

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(K8sVersionDrift))
			})
		})

		Context("Kubelet Client ID", func() {
			It("should NOT trigger drift if node doesn't have kubelet client ID label", func() {
				node.Labels[v1beta1.AKSLabelKubeletIdentityClientID] = "" // Not set

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(BeEmpty())
			})

			It("should trigger drift if node kubelet client ID doesn't match options", func() {
				ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
					KubeletIdentityClientID: lo.ToPtr("3824ff7a-93b6-40af-b861-2eb621ba437a"), // a different random UUID
				}))

				drifted, err := cloudProvider.IsDrifted(ctx, nodeClaim)
				Expect(err).ToNot(HaveOccurred())
				Expect(drifted).To(Equal(KubeletIdentityDrift))
			})
		})

	})

	Context("Mixed Environment - Migration to ProvisionModeAKSMachineAPI", func() {
		var existingVM *armcompute.VirtualMachine

		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
				UseSIG:        lo.ToPtr(true),
			})

			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, azureEnv.SubnetsAPI)
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider)
			cloudProviderNonZonal = New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)

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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			Expect(aksMachineNodeClaim.Name).To(ContainSubstring(createInput.AKSMachineName))
			validateAKSMachineNodeClaim(aksMachineNodeClaim, nodePool)

			// validateVMNodeClaim(vmNodeClaim, nodePool) // Not covered as this fake VM does not have enough data in the first place
			Expect(vmNodeClaim.Status.ProviderID).To(Equal(utils.VMResourceIDToProviderID(ctx, *existingVM.ID)))

			// Get should return AKS machine nodeclaim
			azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			nodeClaim, err := cloudProvider.Get(ctx, aksMachineNodeClaim.Status.ProviderID)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(0)) // Should not be bothered

			//// The returned nodeClaim should be correct
			Expect(nodeClaim).ToNot(BeNil())
			Expect(nodeClaim.Status.Capacity).ToNot(BeEmpty())
			Expect(nodeClaim.Name).To(Equal(createInput.AKSMachineName))
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationAKSMachineResourceID))
			Expect(nodeClaim.Annotations[v1beta1.AnnotationAKSMachineResourceID]).ToNot(BeEmpty())

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
			Expect(nodeClaim.Name).To(ContainSubstring(createInput.AKSMachineName))
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

		Context("Unexpected API failures", func() {
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				azureEnv.SharedStores.AgentPools.Delete(agentPoolID)
				// (then, mostly relying on fake API to reflect the correct behavior)

				// cloudprovider.Get should return NodeClaimNotFoundError, but not panic
				nodeClaim, err := cloudProvider.Get(ctx, aksMachineNodeClaim.Status.ProviderID)
				Expect(err).To(HaveOccurred())
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
				Expect(nodeClaim).To(BeNil())

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
					_, _ = cloudProvider.Create(ctx, nodeClaim)
				}).To(Panic())
			})
		})

	})
})
