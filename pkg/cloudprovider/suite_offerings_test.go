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
	"net/http"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/awslabs/operatorpkg/object"
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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
	"github.com/Azure/skewer"
)

//nolint:gocyclo
func runOfferingTests(provisionMode provisionModeTestCase) {
	Context("Create - Expected Creation Failures", func() {
		It("should fail to provision when LowPriorityCoresQuota errors are hit, then switch capacity type and succeed", func() {
			lowPriorityCoresQuotaErrorMessage := "Operation could not be completed as it results in exceeding approved Low Priority Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22westus2%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22LowPriorityCores%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:32,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22LowPriorityCores%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/per-vm-quota-requests"
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorLowPriorityCoresQuota(fake.Region)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OperationNotAllowed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, lowPriorityCoresQuotaErrorMessage),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				Expect(*createInput.AKSMachine.Properties.Priority).To(Equal(armcontainerservice.ScaleSetPrioritySpot))
				testSKU := fake.MakeSKU(vmSize)
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, testSKU, zone, karpv1.CapacityTypeSpot)
			}

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
			}
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))

			nodes, err := env.KubernetesInterface.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nodes.Items)).To(Equal(1))
			Expect(nodes.Items[0].Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
		})

		It("should fail to provision when OverconstrainedZonalAllocation errors are hit, then switch zone and succeed", func() {
			overconstrainedZonalAllocationErrorMessage := "Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive. Please remove some constraints and try again."
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      karpv1.CapacityTypeLabelKey,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorOverconstrainedZonalAllocation()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OverconstrainedZonalAllocationRequest,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OverconstrainedZonalAllocationRequest, overconstrainedZonalAllocationErrorMessage),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				initialZone, err := instance.GetAKSLabelZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				testSKU := fake.MakeSKU(vmSize)
				ExpectUnavailable(azureEnv, testSKU, initialZone, karpv1.CapacityTypeSpot)

				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(initialZone))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				initialVMSize := string(*vm.Properties.HardwareProfile.VMSize)
				initialCapacityType := instance.GetCapacityTypeFromVM(&vm)
				zone, err := zones.MakeAKSLabelZoneFromVM(&vm)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zone, karpv1.CapacityTypeSpot)

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal(initialVMSize))
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(initialCapacityType))
				Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(zone))
				Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, v1beta1.PlacementScopeZonal))
			}
		})

		It("should fail to provision when OverconstrainedAllocation errors are hit, then switch capacity type and succeed", func() {
			overconstrainedAllocationErrorMessage := "Allocation failed. VM(s) with the following constraints cannot be allocated, because the condition is too restrictive."
			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
				},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelPlacementScope,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{v1beta1.PlacementScopeZonal},
				},
			)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorOverconstrainedAllocation()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OverconstrainedAllocationRequest,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OverconstrainedAllocationRequest, overconstrainedAllocationErrorMessage),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				vmSize := lo.FromPtr(createInput.AKSMachine.Properties.Hardware.VMSize)
				testSKU := fake.MakeSKU(vmSize)
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&createInput.AKSMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, testSKU, zone, karpv1.CapacityTypeSpot)

				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
				Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, v1beta1.PlacementScopeZonal))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				initialVMSize := string(*vm.Properties.HardwareProfile.VMSize)
				initialCapacityType := instance.GetCapacityTypeFromVM(&vm)
				_, err := zones.MakeAKSLabelZoneFromVM(&vm)
				Expect(err).ToNot(HaveOccurred())

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal(initialVMSize))
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).ToNot(Equal(initialCapacityType))
				Expect(node.Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
				Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, v1beta1.PlacementScopeZonal))
			}
		})

		It("should fail to provision when AllocationFailure errors are hit, then switch placement and succeed", func() {
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v3", "Standard_D64s_v3"},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAllocationFailed()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.AllocationFailed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.AllocationFailed, "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				initialVMSize := lo.FromPtr(aksMachine.Properties.Hardware.VMSize)
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&aksMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zone, karpv1.CapacityTypeSpot)

				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal(initialVMSize))
				Expect(node.Labels[v1.LabelTopologyZone]).To(Equal(zones.Regional))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				initialVMSize := string(*vm.Properties.HardwareProfile.VMSize)
				zone, err := zones.MakeAKSLabelZoneFromVM(&vm)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zone, karpv1.CapacityTypeSpot)

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal(initialVMSize))
				Expect(node.Labels[v1.LabelTopologyZone]).To(Equal(zones.Regional))
			}
		})

		It("should fail to provision when AllocationFailure errors are hit and regional placement is unavailable", func() {
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v3"},
			})
			sku := fake.MakeSKU("Standard_D2_v3")
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "RegionalUnavailable", sku, zones.Regional, karpv1.CapacityTypeSpot)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "RegionalUnavailable", sku, zones.Regional, karpv1.CapacityTypeOnDemand)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAllocationFailed()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.AllocationFailed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.AllocationFailed, "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				zone, err := instance.GetAKSLabelZoneFromAKSMachine(&aksMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, sku, zone, karpv1.CapacityTypeSpot)
				ExpectUnavailable(azureEnv, sku, zone, karpv1.CapacityTypeOnDemand)
				ExpectUnavailable(azureEnv, sku, zones.Regional, karpv1.CapacityTypeSpot)
				ExpectUnavailable(azureEnv, sku, zones.Regional, karpv1.CapacityTypeOnDemand)

				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				zone, err := zones.MakeAKSLabelZoneFromVM(&vm)
				Expect(err).ToNot(HaveOccurred())
				ExpectUnavailable(azureEnv, sku, zone, karpv1.CapacityTypeSpot)
				ExpectUnavailable(azureEnv, sku, zone, karpv1.CapacityTypeOnDemand)
				ExpectUnavailable(azureEnv, sku, zones.Regional, karpv1.CapacityTypeSpot)
				ExpectUnavailable(azureEnv, sku, zones.Regional, karpv1.CapacityTypeOnDemand)

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(0))
			}
		})

		It("should fail to provision when VM SKU family vCPU quota exceeded error is returned, and succeed when it is gone", func() {
			familyVCPUQuotaExceededErrorMessage := "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 100, Current Usage: 96, Additional Required: 32, (Minimum) New Limit Required: 128. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22westus2%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22standardDLSv5Family%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:128,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22standardDLSv5Family%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/per-vm-quota-requests"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded("westus2", "Standard NCASv3_T4", 24, 24, 8, 32)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OperationNotAllowed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, familyVCPUQuotaExceededErrorMessage),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
			} else {
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(lo.FromPtr(nic.Interface.ID))
				Expect(ok).To(Equal(false))

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				pod = coretest.UnschedulablePod()
			}

			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})

		It("should fail to provision when VM SKU family vCPU quota limit is zero, and succeed when its gone", func() {
			familyVCPUQuotaIsZeroErrorMessage := "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22westus2%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22standardDLSv5Family%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:128,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22standardDLSv5Family%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/per-vm-quota-requests"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorVMFamilyQuotaExceeded("westus2", "Standard NCASv3_T4", 0, 0, 8, 8)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OperationNotAllowed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, familyVCPUQuotaIsZeroErrorMessage),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
			} else {
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
				Expect(nic).NotTo(BeNil())
				_, ok := azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Load(lo.FromPtr(nic.Interface.ID))
				Expect(ok).To(Equal(false))

				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
				pod = coretest.UnschedulablePod()
			}

			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})

		It("should return ICE if Total Regional Cores Quota errors are hit", func() {
			regionalVCPUQuotaExceededErrorMessage := "Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: uksouth, Current Limit: 100, Current Usage: 100, Additional Required: 64, (Minimum) New Limit Required: 164. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22uksouth%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22cores%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:164,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22cores%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/regional-quota-requests"
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorTotalRegionalCoresQuota(fake.Region)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.OperationNotAllowed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, regionalVCPUQuotaExceededErrorMessage),
						},
					},
				)
			}

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
			if provisionMode.isAKSMachineMode() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass, testNodeClaim)
			} else {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			}

			claim, err := CreateAndWaitForPromises(ctx, cloudProvider, azureEnv, testNodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(claim).To(BeNil())
		})

		It("should fail to provision when AllocationFailure errors are hit and all placements for the VM size are unavailable, then switch VM size and succeed", func() {
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v3", "Standard_D64s_v3"},
			})
			sku := fake.MakeSKU("Standard_D2_v3")
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "RegionalUnavailable", sku, zones.Regional, karpv1.CapacityTypeSpot)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "RegionalUnavailable", sku, zones.Regional, karpv1.CapacityTypeOnDemand)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorAllocationFailed()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{
						ErrorCode: sdkerrors.AllocationFailed,
						RawResponse: &http.Response{
							Body: createSDKErrorBody(sdkerrors.AllocationFailed, "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."),
						},
					},
				)
			}

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			var initialVMSize string
			var zone string
			var err error
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				initialVMSize = lo.FromPtr(aksMachine.Properties.Hardware.VMSize)
				zone, err = instance.GetAKSLabelZoneFromAKSMachine(&aksMachine, fake.Region)
				Expect(err).ToNot(HaveOccurred())
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				initialVMSize = string(*vm.Properties.HardwareProfile.VMSize)
				zone, err = zones.MakeAKSLabelZoneFromVM(&vm)
				Expect(err).ToNot(HaveOccurred())
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
			}
			ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zone, karpv1.CapacityTypeSpot)
			ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zone, karpv1.CapacityTypeOnDemand)
			ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zones.Regional, karpv1.CapacityTypeSpot)
			ExpectUnavailable(azureEnv, fake.MakeSKU(initialVMSize), zones.Regional, karpv1.CapacityTypeOnDemand)

			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels[v1.LabelInstanceTypeStable]).ToNot(Equal(initialVMSize))
		})
	})

	Context("Create - Zone-aware provisioning", func() {
		It("should prefer zonal placement for zone-capable instance types by default", func() {
			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_NC24ads_A100_v4"}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand}},
			)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)

			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, v1beta1.PlacementScopeZonal))
			Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(zones.Regional))

			if provisionMode.isAKSMachineMode() {
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Zones).ToNot(BeEmpty())
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Zones).ToNot(BeEmpty())
			}
		})

		It("should launch zone-capable instance types regionally when placement scope requires it", func() {
			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_NC24ads_A100_v4"}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelPlacementScope,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{v1beta1.PlacementScopeRegional}},
			)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)

			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, zones.Regional))
			Expect(node.Labels).To(HaveKeyWithValue(v1beta1.LabelPlacementScope, v1beta1.PlacementScopeRegional))

			if provisionMode.isAKSMachineMode() {
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Zones).To(BeEmpty())
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Zones).To(BeEmpty())
			}
		})

		It("should launch in the NodePool-requested zone", func() {
			zone, createZone := fmt.Sprintf("%s-3", fake.Region), "3"
			nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeSpot, karpv1.CapacityTypeOnDemand}},
				{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{zone}},
			}
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, zone))

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine).NotTo(BeNil())
				Expect(aksMachine.Zones).To(ConsistOf(&createZone))
			} else {
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm).NotTo(BeNil())
				Expect(vm.Zones).To(ConsistOf(&createZone))
			}
		})

		It("should support provisioning in non-zonal regions", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, azureEnvNonZonal, pod)
			ExpectScheduled(ctx, env.Client, pod)

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnvNonZonal.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnvNonZonal.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Zones).To(BeEmpty())
			} else {
				Expect(azureEnvNonZonal.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnvNonZonal.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Zones).To(BeEmpty())
			}
		})

		It("should support provisioning non-zonal instance types in zonal regions", func() {
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_NC6s_v3"},
			})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)

			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1.LabelTopologyZone, zones.Regional))

			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				Expect(aksMachine.Zones).To(BeEmpty())
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Zones).To(BeEmpty())
			}
		})

		It("should schedule pods with zonal topology spread when non-zonal SKUs exist", func() {
			podLabels := map[string]string{"app": "tsc-repro"}
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pods := []*v1.Pod{}
			for i := 0; i < 3; i++ {
				pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{
					ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
					TopologySpreadConstraints: []v1.TopologySpreadConstraint{
						{
							MaxSkew:           1,
							TopologyKey:       v1.LabelTopologyZone,
							WhenUnsatisfiable: v1.DoNotSchedule,
							LabelSelector:     &metav1.LabelSelector{MatchLabels: podLabels},
						},
					},
				}))
			}
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pods...)
			for _, pod := range pods {
				ExpectScheduled(ctx, env.Client, pod)
			}
		})

		It("should exclude non-zonal instance types via zone NodePool requirements", func() {
			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_NC6s_v3"}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelTopologyZone,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{fakeZone1},
				},
			)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
		})

		It("should exclude non-zonal instance types when all real zones are specified", func() {
			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_NC6s_v3"}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1.LabelTopologyZone,
					Operator: v1.NodeSelectorOpIn,
					Values:   azureEnv.Zones(),
				},
			)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
		})
	})

	Context("Create - Unavailable Offerings", func() {
		It("should not allocate an instance in a zone marked as unavailable", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU("Standard_D2_v2"), fakeZone1, karpv1.CapacityTypeSpot)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU("Standard_D2_v2"), fakeZone1, karpv1.CapacityTypeOnDemand)
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v2"}})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(fakeZone1))
			Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})

		It("should list nodeclaim with correct instance type even after capacity error marks offerings unavailable", func() {
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			var vmSize string
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				aksMachine := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop().AKSMachine
				vmSize = lo.FromPtr(aksMachine.Properties.Hardware.VMSize)
			} else {
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				vmSize = string(lo.FromPtr(vm.Properties.HardwareProfile.VMSize))
			}
			Expect(vmSize).ToNot(BeEmpty())

			for _, zone := range azureEnv.Zones() {
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU(vmSize), zone, karpv1.CapacityTypeOnDemand)
				azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU(vmSize), zone, karpv1.CapacityTypeSpot)
			}

			nodeClaims, err := cloudProvider.List(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClaims).To(HaveLen(1))
			if provisionMode.isAKSMachineMode() {
				validateAKSMachineNodeClaim(nodeClaims[0], nodePool)
			} else {
				validateVMNodeClaim(nodeClaims[0], nodePool)
			}
			Expect(nodeClaims[0].Labels[v1.LabelInstanceTypeStable]).To(Equal(vmSize))
		})

		It("should handle ZonalAllocationFailed on creating the instance", func() {
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorZoneAllocationFailed("Standard_D2_v2", "1")
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(
					&azcore.ResponseError{ErrorCode: sdkerrors.ZoneAllocationFailed},
				)
			}
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
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v2"}})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Reset()
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Reset()
			}
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			retryPod := pod
			if provisionMode.isAKSMachineMode() {
				ExpectNotScheduled(ctx, env.Client, pod)
				Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.CalledWithInput.Len()).To(BeNumerically(">", 0))
			} else {
				// Problem: ExpectProvisionedAndWaitForPromises can bind this pod before the scriptless async VM poll failure is observed.
				// Cleanup is still attempted: VM Delete first does a Get, but this fake poll failure never stores a VM, so BeginDelete cannot be asserted.
				// TODO: Make the fake scriptless async VM failure store the VM before poll failure so this can assert unscheduled pod state and VM BeginDelete.
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineGetBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 2))
			}

			By("marking whatever zone was picked as unavailable - for both spot and on-demand")
			var zone string
			var err error
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">", 0))
				machineInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
				zone, err = instance.GetAKSLabelZoneFromAKSMachine(&machineInput.AKSMachine, fake.Region)
			} else {
				zone, err = zones.MakeAKSLabelZoneFromVM(&azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM)
			}
			Expect(err).ToNot(HaveOccurred())
			for _, skuToCheck := range expectedUnavailableSKUs {
				Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable(skuToCheck, zone, karpv1.CapacityTypeSpot)).To(BeTrue())
				Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable(skuToCheck, zone, karpv1.CapacityTypeOnDemand)).To(BeTrue())
			}

			By("successfully scheduling in a different zone on retry")
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = nil
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(nil)
				// This is the same core test-helper limitation as above: scriptless can observe the VM poll error only after the helper has already fake-scheduled the pod.
				// That scheduled original pod is unusable for validating scriptless retry, because retrying it could pass by reusing the fake-bound failed attempt instead of provisioning again.
				// Machine API keeps the original pod pending in this scenario, so it can retry the same pod; scriptless uses a fresh pod constrained away from the failed zone to force a real retry.
				retryZone, ok := lo.Find(azureEnv.Zones(), func(candidate string) bool { return candidate != zone })
				Expect(ok).To(BeTrue())
				retryPod = coretest.UnschedulablePod(coretest.PodOptions{NodeSelector: map[string]string{v1.LabelTopologyZone: retryZone}})
			}
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, retryPod)
			node := ExpectScheduled(ctx, env.Client, retryPod)
			Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(zone))
			if provisionMode.isAKSMachineMode() {
				Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">", 0))
			}
		})

		It("should launch instances in a different zone than preferred when zone is unavailable", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU("Standard_D2_v2"), fakeZone1, karpv1.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", fake.MakeSKU("Standard_D2_v2"), fakeZone1, karpv1.CapacityTypeSpot)

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
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels[v1.LabelTopologyZone]).ToNot(Equal(fakeZone1))
			Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})

		It("should launch smaller instances than optimal if larger instance launch results in Insufficient Capacity Error", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_F16s_v2"), fakeZone1, karpv1.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_F16s_v2"), fakeZone1, karpv1.CapacityTypeSpot)
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_DS2_v2", "Standard_F16s_v2"}})
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
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pods...)

			nodeNames := sets.New[string]()
			for _, pod := range pods {
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_DS2_v2"))
				nodeNames.Insert(node.Name)
			}
			Expect(nodeNames.Len()).To(Equal(2))
		})

		DescribeTable("should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry",
			func(nonZonal bool) {
				azEnv := azureEnv
				targetCluster := cluster
				targetCloudProvider := cloudProvider
				targetProvisioner := coreProvisioner
				if nonZonal {
					azEnv = azureEnvNonZonal
					targetCluster = clusterNonZonal
					targetCloudProvider = cloudProviderNonZonal
					targetProvisioner = coreProvisionerNonZonal
				}

				for _, zone := range azEnv.Zones() {
					azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zone, karpv1.CapacityTypeSpot)
					azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zone, karpv1.CapacityTypeOnDemand)
				}
				if !nonZonal {
					azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zones.Regional, karpv1.CapacityTypeSpot)
					azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zones.Regional, karpv1.CapacityTypeOnDemand)
				}

				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod(coretest.PodOptions{
					NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
				})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, targetCluster, targetCloudProvider, targetProvisioner, azEnv, pod)
				ExpectNotScheduled(ctx, env.Client, pod)

				azEnv.UnavailableOfferingsCache.Flush()
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, targetCluster, targetCloudProvider, targetProvisioner, azEnv, pod)
				node := ExpectScheduled(ctx, env.Client, pod)
				Expect(node.Labels).To(HaveKeyWithValue(v1.LabelInstanceTypeStable, "Standard_D2_v2"))
			},
			Entry("zonal", false),
			Entry("non-zonal", true),
		)

		It("should mark SKU as unavailable in all zones for Spot", func() {
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorSkuNotAvailable(defaultTestSKU.GetName(), fake.Region)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{ErrorCode: sdkerrors.SKUNotAvailableErrorCode},
				)
			}

			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{defaultTestSKU.GetName()}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeSpot}},
			)
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
			for _, zoneID := range []string{"1", "2", "3"} {
				ExpectUnavailable(azureEnv, defaultTestSKU, zones.MakeAKSLabelZoneFromARMZone(fake.Region, zoneID), karpv1.CapacityTypeSpot)
			}
		})

		It("should mark SKU as unavailable in all zones for OnDemand", func() {
			if provisionMode.isAKSMachineMode() {
				azureEnv.AKSMachinesAPI.AfterPollProvisioningErrorOverride = fake.AKSMachineAPIProvisioningErrorSkuNotAvailable(defaultTestSKU.GetName(), fake.Region)
			} else {
				azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(
					&azcore.ResponseError{ErrorCode: sdkerrors.SKUNotAvailableErrorCode},
				)
			}

			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{defaultTestSKU.GetName()}},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeOnDemand}},
			)
			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod()
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
			for _, zoneID := range []string{"1", "2", "3"} {
				ExpectUnavailable(azureEnv, defaultTestSKU, zones.MakeAKSLabelZoneFromARMZone(fake.Region, zoneID), karpv1.CapacityTypeOnDemand)
			}
		})

		// This is from AKS RP frontend errors rather then CRP (in which Scriptless is calling).
		if provisionMode.isAKSMachineMode() {
			Context("SKUNotAvailable - AKS Machine API sync phase", func() {
				AssertUnavailableSync := func(syncErr *azcore.ResponseError, sku *skewer.SKU, capacityType string) {
					azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.BeginError.Set(syncErr)

					coretest.ReplaceRequirements(nodePool,
						karpv1.NodeSelectorRequirementWithMinValues{
							Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku.GetName()}},
						karpv1.NodeSelectorRequirementWithMinValues{
							Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{capacityType}},
					)
					ExpectApplied(ctx, env.Client, nodeClass, nodePool)
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
					ExpectNotScheduled(ctx, env.Client, pod)
					for _, zoneID := range []string{"1", "2", "3"} {
						ExpectUnavailable(azureEnv, sku, zones.MakeAKSLabelZoneFromARMZone(fake.Region, zoneID), capacityType)
					}
				}

				It("should handle VMSizeNotSupported sync error and mark SKU unavailable", func() {
					AssertUnavailableSync(
						fake.AKSMachineAPIErrorVMSizeNotSupported(lo.FromPtr(defaultTestSKU.Name), azureEnv.SubscriptionID, fake.Region),
						defaultTestSKU, karpv1.CapacityTypeOnDemand,
					)
				})

				It("should handle BadRequest 'not supported for subscription' sync error and mark SKU unavailable", func() {
					AssertUnavailableSync(
						fake.AKSMachineAPIErrorVMSizeNotSupportedBadRequest(lo.FromPtr(defaultTestSKU.Name), azureEnv.SubscriptionID, fake.Region),
						defaultTestSKU, karpv1.CapacityTypeSpot,
					)
				})
			})
		}
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
			cloudProvider.WaitForInstancePromises()
			cluster.Reset()
			clusterNonZonal.Reset()
			azureEnv.Reset(ctx)
			azureEnvNonZonal.Reset(ctx)
		})

		runOfferingTests(aksscriptlessProvisionMode())
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

		runOfferingTests(aksMachineAPIHeaderBatchProvisionMode())
	})
})
