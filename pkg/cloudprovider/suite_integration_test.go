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
	"fmt"
	"net/http"
	"time"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/awslabs/operatorpkg/object"
	corestatus "github.com/awslabs/operatorpkg/status"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	karpv1alpha1 "sigs.k8s.io/karpenter/pkg/apis/v1alpha1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/nodeoverlay"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

func runIntegrationTests(provisionMode provisionModeTestCase) {
	It("should be able to handle basic operations", func() {
		ExpectApplied(ctx, env.Client, nodeClass, nodePool)

		provisionMode.resetListCalls()
		nodeClaims, err := cloudProvider.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClaims).To(BeEmpty())
		provisionMode.expectListCalls()

		provisionMode.resetCreateCalls()
		pod := coretest.UnschedulablePod()
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		provisionMode.expectCreateCalls()
		provisionMode.expectCreatedResource()

		provisionMode.resetListCalls()
		nodeClaims, err = cloudProvider.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		provisionMode.expectListCalls()

		Expect(nodeClaims).To(HaveLen(1))
		createdNodeClaim := nodeClaims[0]
		provisionMode.validateNodeClaim(createdNodeClaim)

		provisionMode.resetGetCalls()
		retrievedNodeClaim, err := cloudProvider.Get(ctx, createdNodeClaim.Status.ProviderID)
		Expect(err).ToNot(HaveOccurred())
		provisionMode.expectGetCalls()

		provisionMode.validateNodeClaim(retrievedNodeClaim)

		provisionMode.resetDeleteCalls()
		Expect(cloudProvider.Delete(ctx, retrievedNodeClaim)).To(Succeed())
		provisionMode.expectDeleteCalls()

		provisionMode.resetListCalls()
		nodeClaims, err = cloudProvider.List(ctx)
		Expect(err).ToNot(HaveOccurred())
		provisionMode.expectListCalls()
		Expect(nodeClaims).To(BeEmpty())

		provisionMode.resetGetCalls()
		nodeClaim, err = cloudProvider.Get(ctx, createdNodeClaim.Status.ProviderID)
		Expect(err).To(HaveOccurred())
		Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		provisionMode.expectGetCalls()
		Expect(nodeClaim).To(BeNil())
	})

	runNodeOverlayCapacityTests(nodeOverlayCapacityTestOptions{
		validateNodeClaim: provisionMode.validateNodeClaim,
		resetCreateCalls:  provisionMode.resetCreateCalls,
		expectCreateCalls: provisionMode.expectCreateCalls,
	})

	Context("Create - CloudProvider Create Error Cases", func() {
		It("should return error when NodeClass readiness is Unknown", func() {
			nodeClass.StatusConditions().SetUnknown(corestatus.ConditionReady)
			testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

			ExpectApplied(ctx, env.Client, nodePool, nodeClass, testNodeClaim)
			claim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, testNodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
			Expect(claim).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("resolving NodeClass readiness, NodeClass is in Ready=Unknown"))
		})

		It("should return error when instance creation fails", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

			expectedErrorMessage := "creating instance failed"
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAny()
				expectedErrorMessage = "creating AKS machine failed"
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OperationNotAllowed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, "Failed to create VM"),
						},
					},
				)
			}

			claim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, testNodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
			Expect(claim).To(BeNil())
			Expect(err.Error()).To(ContainSubstring(expectedErrorMessage))
		})

		It("should return error when instance type resolution fails", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			azureEnv.InstanceTypesProvider.Reset()

			testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
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

			claim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, testNodeClaim)
			Expect(err).To(HaveOccurred())
			Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.CreateError{}))
			Expect(claim).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("resolving instance types"))

			Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
		})

		It("should return an ICE error when there are no instance types to launch", func() {
			// Specify no instance types and expect to receive a capacity error
			nodeClaim.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"doesnotexist"}, // will not match any instance types,
				},
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass, nodeClaim)
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(cloudProviderMachine).To(BeNil())
		})

		if !provisionMode.isAKSMachineMode() {
			// TODO: share this with Machine API mode
			It("should not reattempt creation of a vm thats been created before", func() {
				testNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
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

			// NIC handling is delegated to Machine API
			It("should delete the network interface on failure to create the vm", func() {
				errMsg := "test error"
				errCode := fmt.Sprint(http.StatusNotFound)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: errCode,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(errCode, errMsg),
						},
					},
				)
				pod := coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(lo.FromPtr(nic.Interface.ID))
				Expect(ok).To(Equal(false))

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				pod = coretest.UnschedulablePod()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
			})
		}
	})

	runUnhappyPathHandlingTests(provisionMode)
}

func reconcileCapacityOverlay(customResource v1.ResourceName, overlayCapacity resource.Quantity) {
	GinkgoHelper()
	nodeOverlay := coretest.NodeOverlay(karpv1alpha1.NodeOverlay{
		Spec: karpv1alpha1.NodeOverlaySpec{
			Requirements: []karpv1alpha1.NodeSelectorRequirement{{
				Key:      karpv1.NodePoolLabelKey,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{nodePool.Name},
			}},
			Capacity: v1.ResourceList{customResource: overlayCapacity},
		},
	})
	ExpectApplied(ctx, env.Client, nodeOverlay)
	nodeOverlayController := nodeoverlay.NewController(env.Client, cloudProvider, azureEnv.InstanceTypeStore, cluster)
	ExpectReconcileSucceeded(ctx, nodeOverlayController, client.ObjectKeyFromObject(nodeOverlay))
}

type nodeOverlayCapacityTestOptions struct {
	validateNodeClaim func(*karpv1.NodeClaim)
	resetCreateCalls  func()
	expectCreateCalls func()
}

func runNodeOverlayCapacityTests(testOptions nodeOverlayCapacityTestOptions) {
	Context("NodeOverlay", func() {
		It("should launch a NodeClaim that requests capacity added by a NodeOverlay", func() {
			ctx = coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates: coretest.FeatureGates{NodeOverlay: lo.ToPtr(true)},
			}))
			customResource := v1.ResourceName("example.com/dongle")
			overlayCapacity := resource.MustParse("100")
			nodeClaim.Spec.Resources.Requests = v1.ResourceList{customResource: resource.MustParse("1")}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClaim)
			reconcileCapacityOverlay(customResource, overlayCapacity)

			if testOptions.resetCreateCalls != nil {
				testOptions.resetCreateCalls()
			}
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(cloudProviderMachine).ToNot(BeNil())
			if testOptions.validateNodeClaim != nil {
				testOptions.validateNodeClaim(cloudProviderMachine)
			}
			if testOptions.expectCreateCalls != nil {
				testOptions.expectCreateCalls()
			}
			capacity, ok := cloudProviderMachine.Status.Capacity[customResource]
			Expect(ok).To(BeTrue())
			Expect(capacity.Cmp(overlayCapacity)).To(Equal(0))
			allocatable, ok := cloudProviderMachine.Status.Allocatable[customResource]
			Expect(ok).To(BeTrue())
			Expect(allocatable.Cmp(overlayCapacity)).To(Equal(0))
		})

		It("should not use overlaid capacity when NodeOverlay is disabled", func() {
			// Explicitly disable the NodeOverlay feature gate so this test does not
			// depend on ordering with the previous It block that enables it.
			ctx = coreoptions.ToContext(ctx, coretest.Options(coretest.OptionsFields{
				FeatureGates: coretest.FeatureGates{NodeOverlay: lo.ToPtr(false)},
			}))
			customResource := v1.ResourceName("example.com/dongle")
			overlayCapacity := resource.MustParse("100")
			nodeClaim.Spec.Resources.Requests = v1.ResourceList{customResource: resource.MustParse("1")}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool, nodeClaim)
			reconcileCapacityOverlay(customResource, overlayCapacity)

			if testOptions.resetCreateCalls != nil {
				testOptions.resetCreateCalls()
			}
			cloudProviderMachine, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, nodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(cloudProviderMachine).To(BeNil())
		})
	})
}

//nolint:gocyclo
func runUnhappyPathHandlingTests(provisionMode provisionModeTestCase) {
	Context("Unexpected API Failures", func() {
		It("should handle create failures - unrecognized error during sync/initial", func() {
			// Set up error to occur immediately during create.
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)
			}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			// Verify the create API was called and cleanup was attempted where applicable.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(nil)
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
			}

			// Verify provisioning works again after clearing the error.
			pod2 := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod2)
			ExpectScheduled(ctx, env.Client, pod2)
		})

		It("should handle create failures - unrecognized error during async/LRO", func() {
			// Set up error to occur during async provisioning.
			if provisionMode.isAKSMachineMode() {
				// WARNING: This fake currently surfaces through the immediate post-create GET, not the AKSMachine async poller.
				// TODO: Make AfterPollProvisioningErrorOverride fail through the AKSMachine async poller path instead.
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAny()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			}

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)

			if provisionMode.isAKSMachineMode() {
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
			} else {
				// Problem: async failure doesn't affect schedulability due to the limitations of core test framework
				// Machine API mode doesn't have the problem because a different problem of async failure simulation get caught in post-create GET hides it (details above)
				//ExpectNotScheduled(ctx, env.Client, pod)
				// Cleanup is invoked, but this fake async VM failure returns the poller error before storing a fake VM, so VM-provider Delete sees not found and no VM BeginDelete call is observable.
				// TODO: Make the fake async VM failure store the VM before poll failure so this test can validate VM cleanup like Machine API validates DeleteMachines below.
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(nil)
			}

			// Verify provisioning works again after clearing the error.
			pod2 := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod2)
			ExpectScheduled(ctx, env.Client, pod2)
		})

		It("should handle get failures - unrecognized error", func() {
			// First create a successful nodeclaim.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim.
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			provisionMode.validateNodeClaim(nodeClaims[0])

			// Set up get to fail.
			provisionMode.resetGetCalls()
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			}

			// Attempt to get the nodeclaim - should fail.
			retrievedNodeClaim, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
			Expect(err).To(HaveOccurred())
			Expect(retrievedNodeClaim).To(BeNil())

			// Verify the get API was called, then clear the error for cleanup.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(nil)
			}
		})

		It("should handle delete failures - unrecognized error during sync/initial", func() {
			// First create a successful nodeclaim.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim.
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			provisionMode.validateNodeClaim(nodeClaims[0])

			// Set up delete to fail.
			provisionMode.resetDeleteCalls()
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.BeginError.Set(fake.AKSMachineAPIErrorAny)
			}

			// Attempt to delete the nodeclaim - should fail.
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).To(HaveOccurred())

			// Verify the delete API was called, then clear the error for cleanup.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.BeginError.Set(nil)
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.BeginError.Set(nil)
			}
		})

		It("should handle delete failures - unrecognized error during async/LRO", func() {
			// First create a successful nodeclaim.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Get the created nodeclaim.
			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			provisionMode.validateNodeClaim(nodeClaims[0])

			// Set up delete to fail.
			provisionMode.resetDeleteCalls()
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			}

			// Attempt to delete the nodeclaim - should fail.
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).To(HaveOccurred())

			// Verify the delete API was called, then clear the error for cleanup.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.Error.Set(nil)
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.Error.Set(nil)
			}
		})

		It("should handle list failures - unrecognized error", func() {
			// Under batch, List must work for PollUntilDone (provisioning) to complete.
			// Testing "List fails" requires a batch-specific test that expects provisioning failure,
			// not this test which assumes provisioning succeeds then List fails afterward.
			if provisionMode.isAKSMachineAPIHeaderBatchMode() {
				Skip("header-batch mode lists AKS machines during provisioning")
			}

			// First create a successful nodeclaim.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			provisionMode.resetListCalls()
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			} else {
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.Error.Set(fake.AKSMachineAPIErrorAny)
			}

			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).To(HaveOccurred())
			Expect(nodeClaims).To(BeEmpty())

			// Verify the list API was called, then clear the error for cleanup.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.AKSMachinesAPI.AKSMachineNewListPagerBehavior.Error.Set(nil)
			} else {
				Expect(azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.AzureResourceGraphAPI.AzureResourceGraphResourcesBehavior.Error.Set(nil)
			}

			// Verify provisioning works again after clearing the error.
			pod2 := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod2)
			ExpectScheduled(ctx, env.Client, pod2)
		})
	})

	// We currently don't support changing immutable offerings requirements for an already-created nodeclaim name.
	Context("Operation Conflicts/Races", func() {
		It("should handle get/delete failures - not found/already deleted externally", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			provisionMode.validateNodeClaim(nodeClaims[0])

			// Delete the nodeclaim so the backing instance is gone.
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).ToNot(HaveOccurred())

			provisionMode.resetGetCalls()
			// Get should return NodeClaimNotFound after the backing instance is gone.
			retrievedNodeClaim2, err := cloudProvider.Get(ctx, nodeClaims[0].Status.ProviderID)
			Expect(err).To(HaveOccurred())
			provisionMode.expectGetCalls()
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
			Expect(retrievedNodeClaim2).To(BeNil())

			provisionMode.resetGetCalls()
			provisionMode.resetDeleteCalls()
			// Attempt to delete the nodeclaim again - should fail as not found.
			err = cloudProvider.Delete(ctx, nodeClaims[0])
			Expect(err).To(HaveOccurred())
			provisionMode.expectGetCalls()
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(Equal(0))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(Equal(0))
			}
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})

		It("should handle instance create - found in get, with the same requirements", func() {
			// Create a fresh nodeclaim with explicit requirements so we know exactly what it will have.
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
							Key:      v1.LabelTopologyZone,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")},
						},
						{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D2_v2"},
						},
					},
				},
			})

			// First create a successful instance using cloudProvider.Create directly.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
			createdFirstNodeClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, firstNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdFirstNodeClaim).ToNot(BeNil())
			provisionMode.validateNodeClaim(createdFirstNodeClaim)
			Expect(createdFirstNodeClaim.CreationTimestamp).ToNot(BeZero())

			// Create a conflicted nodeclaim with the same configuration.
			conflictedNodeClaim := firstNodeClaim.DeepCopy()

			// Reset API call tracking before triggering the reuse path.
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.CalledWithInput.Reset()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			}

			// Call Create with the same-named nodeclaim to trigger get/reuse.
			createdNodeClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdNodeClaim).ToNot(BeNil())

			// Verify the existing instance was reused successfully.
			if provisionMode.isAKSMachineMode() {
				// With cache enabled, the pre-create GET is served from cache, so no new create is recorded.
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				// Since no new instance was created, verify the fake store still has the original configuration.
				machineID := fake.MkMachineID(testOptions.NodeResourceGroup, testOptions.ClusterName, testOptions.AKSMachinesPoolName, firstNodeClaim.Name)
				aksMachine, ok := azureEnv.AKSDataStorage.AKSMachines.Load(machineID)
				Expect(ok).To(BeTrue(), "AKS machine should exist in fake store")
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal("Standard_D2_v2"))
				Expect(aksMachine.Zones).To(HaveLen(1))
				Expect(*aksMachine.Zones[0]).To(Equal("1"))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(Equal(1))
				vmName := instance.GenerateResourceName(firstNodeClaim.Name)
				vm, ok := azureEnv.VirtualMachinesAPI.Instances.Load(fake.MkVMID(testOptions.NodeResourceGroup, vmName))
				Expect(ok).To(BeTrue(), "VM should exist in fake store")
				Expect(vm.Properties).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile.VMSize).ToNot(BeNil())
				Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal("Standard_D2_v2"))
				Expect(vm.Zones).To(HaveLen(1))
				Expect(lo.FromPtr(vm.Zones[0])).To(Equal("1"))
			}

			// Validate the returned nodeclaim has the expected configuration.
			provisionMode.validateNodeClaim(createdNodeClaim)
			Expect(createdNodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")))
			Expect(createdNodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})

		It("should handle instance create - not found in get, but found during create with the same requirements", func() {
			// Create a fresh nodeclaim with explicit requirements so we know exactly what it will have.
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
							Key:      v1.LabelTopologyZone,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")},
						},
						{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D2_v2"},
						},
					},
				},
			})

			// First create a successful instance using cloudProvider.Create directly.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
			createdFirstNodeClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, firstNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdFirstNodeClaim).ToNot(BeNil())
			provisionMode.validateNodeClaim(createdFirstNodeClaim)
			Expect(createdFirstNodeClaim.CreationTimestamp).ToNot(BeZero())

			// Create a conflicted nodeclaim with the same configuration.
			conflictedNodeClaim := firstNodeClaim.DeepCopy()

			// Simulate get missing the existing instance before create finds the same configuration.
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorFromAKSMachineNotFound)
				azureEnv.AKSMachineCache.InvalidateAll()
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				vmName := instance.GenerateResourceName(firstNodeClaim.Name)
				vm, ok := azureEnv.VirtualMachinesAPI.Instances.Load(fake.MkVMID(testOptions.NodeResourceGroup, vmName))
				Expect(ok).To(BeTrue(), "VM should exist in fake store")
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Output.Set(&armcompute.VirtualMachinesClientCreateOrUpdateResponse{VirtualMachine: vm})
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(&azcore.ResponseError{StatusCode: http.StatusNotFound})
			}

			// Call Create with the same-named nodeclaim to trigger create-after-get-miss handling.
			createdNodeClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdNodeClaim).ToNot(BeNil())

			// Verify the instance was created successfully.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal("Standard_D2_v2"))
				Expect(aksMachine.Zones).To(HaveLen(1))
				Expect(*aksMachine.Zones[0]).To(Equal("1"))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Output.Set(nil)
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(nil)
				vmName := instance.GenerateResourceName(firstNodeClaim.Name)
				vm, ok := azureEnv.VirtualMachinesAPI.Instances.Load(fake.MkVMID(testOptions.NodeResourceGroup, vmName))
				Expect(ok).To(BeTrue(), "VM should exist in fake store")
				Expect(vm.Properties).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
				Expect(vm.Properties.HardwareProfile.VMSize).ToNot(BeNil())
				Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal("Standard_D2_v2"))
				Expect(vm.Zones).To(HaveLen(1))
				Expect(lo.FromPtr(vm.Zones[0])).To(Equal("1"))
			}

			// Validate the returned nodeclaim has the expected configuration.
			provisionMode.validateNodeClaim(createdNodeClaim)
			Expect(createdNodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")))
			Expect(createdNodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})

		It("should handle instance create - not found in get, but found during create with conflicted requirements", func() {
			// Create a fresh nodeclaim with explicit requirements so we know exactly what it will have.
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
							Key:      v1.LabelTopologyZone,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{zones.MakeAKSLabelZoneFromARMZone(fake.Region, "1")},
						},
						{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"Standard_D2_v2"},
						},
					},
				},
			})

			// First create a successful instance using cloudProvider.Create directly.
			ExpectApplied(ctx, env.Client, nodeClass, nodePool, firstNodeClaim)
			createdFirstNodeClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, firstNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdFirstNodeClaim).ToNot(BeNil())
			provisionMode.validateNodeClaim(createdFirstNodeClaim)
			Expect(createdFirstNodeClaim.CreationTimestamp).ToNot(BeZero())

			expectedConflictedVMSize := "Standard_D2_v2"

			// Create a conflicted nodeclaim with different immutable configuration.
			// Change zone to create the immutable configuration conflict.
			conflictedNodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:   firstNodeClaim.Name,
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
							Key:      v1.LabelTopologyZone,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{zones.MakeAKSLabelZoneFromARMZone(fake.Region, "2")},
						},
						{
							Key:      v1.LabelInstanceTypeStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{expectedConflictedVMSize},
						},
					},
				},
			})

			// Simulate get missing the existing instance before create hits conflicting requirements.
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(fake.AKSMachineAPIErrorFromAKSMachineNotFound)
				azureEnv.AKSMachineCache.InvalidateAll()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(&azcore.ResponseError{StatusCode: http.StatusNotFound})
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
				azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Reset()
			}

			// Call Create with the conflicted nodeclaim to trigger cleanup of the conflicting instance.
			if provisionMode.isAKSMachineMode() {
				_, err = CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
				Expect(err).To(HaveOccurred())
			} else {
				freshNodeClaim := &karpv1.NodeClaim{}
				Expect(env.Client.Get(ctx, client.ObjectKey{Name: conflictedNodeClaim.Name}, freshNodeClaim)).To(Succeed())
				freshNodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
				Expect(env.Client.Status().Update(ctx, freshNodeClaim)).To(Succeed())
				_, err = cloudProvider.Create(ctx, conflictedNodeClaim)
				Expect(err).ToNot(HaveOccurred())
				cloudProvider.WaitForInstancePromises()
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				conflictCreateInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(conflictCreateInput.VM.Properties).ToNot(BeNil())
				Expect(conflictCreateInput.VM.Properties.HardwareProfile).ToNot(BeNil())
				Expect(conflictCreateInput.VM.Properties.HardwareProfile.VMSize).ToNot(BeNil())
				Expect(string(lo.FromPtr(conflictCreateInput.VM.Properties.HardwareProfile.VMSize))).To(Equal(expectedConflictedVMSize))
				Expect(conflictCreateInput.VM.Zones).To(HaveLen(1))
				Expect(lo.FromPtr(conflictCreateInput.VM.Zones[0])).To(Equal("2"))
			}

			// Verify cleanup was attempted after the conflict, then clear the injected get error.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.AKSMachinesAPI.AKSMachineGetBehavior.Error.Set(nil)
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				deleteInput := azureEnv.VirtualMachinesAPI.VirtualMachineDeleteBehavior.CalledWithInput.Pop()
				Expect(deleteInput.VMName).To(Equal(instance.GenerateResourceName(conflictedNodeClaim.Name)))
				azureEnv.VirtualMachinesAPI.Instances.Delete(fake.MkVMID(deleteInput.ResourceGroupName, deleteInput.VMName))
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.Error.Set(nil)
			}

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Reset()
			}
			// Retry should succeed after the conflicting instance is gone.
			createdConflictedNodeClaim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, conflictedNodeClaim)
			Expect(err).ToNot(HaveOccurred())
			Expect(createdConflictedNodeClaim).ToNot(BeNil())

			// Verify the instance was created successfully with the conflicted configuration.
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				aksMachine := createInput.AKSMachine
				Expect(aksMachine.Properties).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware).ToNot(BeNil())
				Expect(aksMachine.Properties.Hardware.VMSize).ToNot(BeNil())
				Expect(*aksMachine.Properties.Hardware.VMSize).To(Equal(expectedConflictedVMSize))
				Expect(aksMachine.Zones).To(HaveLen(1))
				Expect(*aksMachine.Zones[0]).To(Equal("2"))
				Expect(createdConflictedNodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(zones.MakeAKSLabelZoneFromARMZone(fake.Region, "2")))
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(createInput.VM.Properties).ToNot(BeNil())
				Expect(createInput.VM.Properties.HardwareProfile).ToNot(BeNil())
				Expect(createInput.VM.Properties.HardwareProfile.VMSize).ToNot(BeNil())
				Expect(string(lo.FromPtr(createInput.VM.Properties.HardwareProfile.VMSize))).To(Equal(expectedConflictedVMSize))
				Expect(createInput.VM.Zones).To(HaveLen(1))
				retriedZoneID := lo.FromPtr(createInput.VM.Zones[0])
				Expect(retriedZoneID).ToNot(BeEmpty())
				vm, ok := azureEnv.VirtualMachinesAPI.Instances.Load(fake.MkVMID(createInput.ResourceGroupName, createInput.VMName))
				Expect(ok).To(BeTrue(), "VM should exist in fake store")
				Expect(string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))).To(Equal(expectedConflictedVMSize))
				Expect(createdConflictedNodeClaim.Labels[v1.LabelTopologyZone]).To(Equal(zones.MakeAKSLabelZoneFromARMZone(fake.Region, retriedZoneID)))
			}

			// Validate the returned nodeclaim has the expected configuration.
			provisionMode.validateNodeClaim(createdConflictedNodeClaim)
			Expect(createdConflictedNodeClaim.Labels[v1.LabelInstanceTypeStable]).To(Equal(expectedConflictedVMSize))
			Expect(createdConflictedNodeClaim.CreationTimestamp).ToNot(BeZero())
		})
	})
}

var _ = Describe("CloudProvider", func() {
	Context("ProvisionMode = AKSScriptless, ManageExistingAKSMachines = false", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSScriptless),
				ManageExistingAKSMachines: lo.ToPtr(false),
			})
			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset(ctx)
		})

		runIntegrationTests(aksscriptlessProvisionMode())
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
			test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)
			cloudProvider = New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, recorder, env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)

			cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
			coreProvisioner = provisioning.NewProvisioner(env.Client, recorder, cloudProvider, cluster, fakeClock)
		})

		AfterEach(func() {
			// Wait for any async polling goroutines to complete before resetting
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			azureEnv.Reset(ctx)
		})

		runIntegrationTests(aksscriptlessProvisionMode())
	})

	Context("ProvisionMode = AKSMachineAPIHeaderBatch, ManageExistingAKSMachines = false", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSMachineAPIHeaderBatch),
				UseSIG:                    lo.ToPtr(true),
				ManageExistingAKSMachines: lo.ToPtr(false), // should not have any effect, as ProvisionMode is AKSMachineAPI
			})

			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, nil, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
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

		runIntegrationTests(aksMachineAPIHeaderBatchProvisionMode())
	})

	Context("ProvisionMode = AKSMachineAPIHeaderBatch, ManageExistingAKSMachines = true", func() {
		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode:             lo.ToPtr(consts.ProvisionModeAKSMachineAPIHeaderBatch),
				UseSIG:                    lo.ToPtr(true),
				ManageExistingAKSMachines: lo.ToPtr(true), // should not have any effect
			})

			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, nil, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
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

		runIntegrationTests(aksMachineAPIHeaderBatchProvisionMode())
	})

	Context("Mixed Environment - Migration from ProvisionMode = AKSMachineAPIHeaderBatch to VM mode", func() {
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
				statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, nil, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
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
				azureEnv.Reset(ctx)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
				statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, nil, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
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
				azureEnv.Reset(ctx)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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

	Context("Mixed Environment - Migration to ProvisionMode = AKSMachineAPIHeaderBatch", func() {
		var existingVM *armcompute.VirtualMachine

		BeforeEach(func() {
			testOptions = test.Options(test.OptionsFields{
				ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPIHeaderBatch),
				UseSIG:        lo.ToPtr(true),
			})

			ctx = coreoptions.ToContext(ctx, coretest.Options())
			ctx = options.ToContext(ctx, testOptions)

			azureEnv = test.NewEnvironment(ctx, env)
			azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
			statusController = status.NewController(env.Client, azureEnv.KubernetesVersionProvider, azureEnv.ImageProvider, env.KubernetesInterface, env.KubernetesInterface, nil, azureEnv.SubnetsAPI, azureEnv.DiskEncryptionSetsAPI, testOptions.ParsedDiskEncryptionSetID, options.FromContext(ctx).NetworkPolicy, options.FromContext(ctx).NetworkPlugin)
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
			azureEnv.Reset(ctx)
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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
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
})
