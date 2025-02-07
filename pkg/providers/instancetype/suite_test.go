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

package instancetype_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/awslabs/operatorpkg/status"
	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	. "knative.dev/pkg/logging/testing"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/imagefamily/bootstrap"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv, azureEnvNonZonal *test.Environment
var fakeClock *clock.FakeClock
var coreProvisioner, coreProvisionerNonZonal *provisioning.Provisioner
var cluster, clusterNonZonal *state.Cluster
var cloudProvider, cloudProviderNonZonal *cloudprovider.CloudProvider

var fakeZone1 = utils.MakeZone(fake.Region, "1")

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())

	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)

	fakeClock = &clock.FakeClock{}
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)

	cluster = state.NewCluster(fakeClock, env.Client)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProviderNonZonal, clusterNonZonal, fakeClock)

	RunSpecs(t, "Provider/Azure")
}

var _ = BeforeSuite(func() {
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("InstanceType Provider", func() {
	var nodeClass *v1alpha2.AKSNodeClass
	var nodePool *karpv1.NodePool

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodeClass.StatusConditions().SetTrue(status.ConditionReady)
		nodePool = coretest.NodePool(karpv1.NodePool{
			Spec: karpv1.NodePoolSpec{
				Template: karpv1.NodeClaimTemplate{
					Spec: karpv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Name: nodeClass.Name,
						},
					},
				},
			},
		})

		ctx = options.ToContext(ctx, test.Options())
		cluster.Reset()
		clusterNonZonal.Reset()
		azureEnv.Reset()
		azureEnvNonZonal.Reset()
	})

	AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	Context("Subnet", func() {
		It("should use the VNET_SUBNET_ID", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
			Expect(nic).NotTo(BeNil())
			Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpentervnet/subnets/karpentersub"))
		})
		It("should produce all required azure cni labels", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			decodedString := ExpectDecodedCustomData(azureEnv)
			Expect(decodedString).To(SatisfyAll(
				ContainSubstring("kubernetes.azure.com/ebpf-dataplane=cilium"),
				ContainSubstring("kubernetes.azure.com/network-subnet=karpentersub"),
				ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=a519e60a-cac0-40b2-b883-084477fe6f5c"),
				ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
			))
		})
		It("should use the subnet specified in the nodeclass", func() {
			nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpenter/subnets/nodeclassSubnet")
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
			Expect(nic).NotTo(BeNil())
			Expect(lo.FromPtr(nic.Interface.Properties.IPConfigurations[0].Properties.Subnet.ID)).To(Equal("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/karpenter/subnets/nodeclassSubnet"))
		})
	})
	Context("VM Creation Failures", func() {
		It("should delete the network interface on failure to create the vm", func() {
			ErrMsg := "test error"
			ErrCode := fmt.Sprint(http.StatusNotFound)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{
					ErrorCode: ErrCode,
					RawResponse: &http.Response{
						Body: createSDKErrorBody(ErrCode, ErrMsg),
					},
				},
			)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})
		It("should fail to provision when LowPriorityCoresQuota errors are hit, then switch capacity type and succeed", func() {
			LowPriorityCoresQuotaErrorMessage := "Operation could not be completed as it results in exceeding approved Low Priority Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22westus2%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22LowPriorityCores%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:32,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22LowPriorityCores%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/per-vm-quota-requests"
			// Create nodepool that has both ondemand and spot capacity types enabled
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand, karpv1.CapacityTypeSpot},
				}})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			// Set the LowPriorityCoresQuota error to be returned when creating the vm
			azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{
					ErrorCode: sdkerrors.OperationNotAllowed,
					RawResponse: &http.Response{
						Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, LowPriorityCoresQuotaErrorMessage),
					},
				},
			)
			// Create a pod that should fail to schedule
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectNotScheduled(ctx, env.Client, pod)
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(nil)
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Expect that on-demand nodes are selected if spot capacity is unavailable, and the nodepool uses both spot + on-demand
			nodes, err := env.KubernetesInterface.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nodes.Items)).To(Equal(1))
			Expect(nodes.Items[0].Labels[karpv1.CapacityTypeLabelKey]).To(Equal(karpv1.CapacityTypeOnDemand))
		})

		It("should fail to provision when VM SKU family vCPU quota exceeded error is returned, and succeed when it is gone", func() {
			familyVCPUQuotaExceededErrorMessage := "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 100, Current Usage: 96, Additional Required: 32, (Minimum) New Limit Required: 128. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22westus2%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22standardDLSv5Family%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:128,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22standardDLSv5Family%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/per-vm-quota-requests"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{
					ErrorCode: sdkerrors.OperationNotAllowed,
					RawResponse: &http.Response{
						Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, familyVCPUQuotaExceededErrorMessage),
					},
				},
			)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})
		It("should fail to provision when VM SKU family vCPU quota limit is zero, and succeed when its gone", func() {
			familyVCPUQuotaIsZeroErrorMessage := "Operation could not be completed as it results in exceeding approved standardDLSv5Family Cores quota. Additional details - Deployment Model: Resource Manager, Location: westus2, Current Limit: 0, Current Usage: 0, Additional Required: 32, (Minimum) New Limit Required: 32. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22westus2%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22standardDLSv5Family%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:128,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22standardDLSv5Family%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/per-vm-quota-requests"
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{
					ErrorCode: sdkerrors.OperationNotAllowed,
					RawResponse: &http.Response{
						Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, familyVCPUQuotaIsZeroErrorMessage),
					},
				},
			)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})

		It("should return ICE if Total Regional Cores Quota errors are hit", func() {
			regionalVCPUQuotaExceededErrorMessage := "Operation could not be completed as it results in exceeding approved Total Regional Cores quota. Additional details - Deployment Model: Resource Manager, Location: uksouth, Current Limit: 100, Current Usage: 100, Additional Required: 64, (Minimum) New Limit Required: 164. Submit a request for Quota increase at https://aka.ms/ProdportalCRP/#blade/Microsoft_Azure_Capacity/UsageAndQuota.ReactView/Parameters/%7B%22subscriptionId%22:%(redacted)%22,%22command%22:%22openQuotaApprovalBlade%22,%22quotas%22:[%7B%22location%22:%22uksouth%22,%22providerId%22:%22Microsoft.Compute%22,%22resourceName%22:%22cores%22,%22quotaRequest%22:%7B%22properties%22:%7B%22limit%22:164,%22unit%22:%22Count%22,%22name%22:%7B%22value%22:%22cores%22%7D%7D%7D%7D]%7D by specifying parameters listed in the ‘Details’ section for deployment to succeed. Please read more about quota limits at https://docs.microsoft.com/en-us/azure/azure-supportability/regional-quota-requests"
			azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{
					ErrorCode: sdkerrors.OperationNotAllowed,
					RawResponse: &http.Response{
						Body: createSDKErrorBody(sdkerrors.OperationNotAllowed, regionalVCPUQuotaExceededErrorMessage),
					},
				},
			)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						karpv1.NodePoolLabelKey: nodePool.Name,
					},
				},
				Spec: karpv1.NodeClaimSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Name: nodeClass.Name,
					},
				},
			})
			claim, err := cloudProvider.Create(ctx, nodeClaim)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(claim).To(BeNil())

		})
	})

	Context("Filtering in InstanceType Provider List", func() {
		var instanceTypes corecloudprovider.InstanceTypes
		var err error
		getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

		BeforeEach(func() {
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not include SKUs marked as restricted", func() {
			isRestricted := func(instanceType *corecloudprovider.InstanceType) bool {
				return instancetype.RestrictedVMSizes.Has(instanceType.Name)
			}
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(isRestricted, Equal(true))))
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(isRestricted, Equal(true))))
		})
		It("should not include SKUs with constrained CPUs, but include unconstrained ones", func() {
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_M8-2ms"))))
			Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_D2_v2"))))
		})
		It("should not include confidential SKUs", func() {
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_DC8s_v3"))))
		})
		It("should not include SKUs without compatible image", func() {
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_D2as_v6"))))
		})
	})
	Context("Filtering GPU SKUs ProviderList(AzureLinux)", func() {
		var instanceTypes corecloudprovider.InstanceTypes
		var err error
		getName := func(instanceType *corecloudprovider.InstanceType) string { return instanceType.Name }

		BeforeEach(func() {
			nodeClassAZLinux := test.AKSNodeClass()
			nodeClassAZLinux.Spec.ImageFamily = lo.ToPtr("AzureLinux")
			ExpectApplied(ctx, env.Client, nodeClassAZLinux)
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClassAZLinux)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not include AKSUbuntu GPU SKUs in list results", func() {
			Expect(instanceTypes).ShouldNot(ContainElement(WithTransform(getName, Equal("Standard_NC24ads_A100_v4"))))
		})
		It("should include AKSUbuntu GPU SKUs in list results", func() {
			Expect(instanceTypes).Should(ContainElement(WithTransform(getName, Equal("Standard_NC16as_T4_v3"))))
		})
	})

	Context("Ephemeral Disk", func() {
		It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
			// Create a Provisioner that selects a sku that supports ephemeral
			// SKU Standard_D64s_v3 has 1600GB of CacheDisk space, so we expect we can create an ephemeral disk with size 128GB
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      "node.kubernetes.io/instance-type",
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_D64s_v3"},
				}})
			np.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
				Name: nodeClass.Name,
			}

			ExpectApplied(ctx, env.Client, np, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
			// should have local disk attached
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).NotTo(BeNil())
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
		})

		It("should use ephemeral disk if supported, and set disk size to OSDiskSizeGB from node class", func() {
			// Create a Nodepool that selects a sku that supports ephemeral
			// SKU Standard_D64s_v3 has 1600GB of CacheDisk space, so we expect we can create an ephemeral disk with size 256GB
			nodeClass = test.AKSNodeClass()
			nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](256)
			nodeClass.StatusConditions().SetTrue(status.ConditionReady)
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      "node.kubernetes.io/instance-type",
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_D64s_v3"},
				}})
			np.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
				Name: nodeClass.Name,
			}

			ExpectApplied(ctx, env.Client, np, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(256)))
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
		})
		It("should not use ephemeral disk if ephemeral is supported, but we don't have enough space", func() {
			// Create a Nodepool that selects a sku that supports ephemeral Standard_D2s_v3
			// Standard_D2s_V3 has 53GB Of CacheDisk space,
			// and has 16GB of Temp Disk Space.
			// With our rule of 100GB being the minimum OSDiskSize, this VM should be created without local disk
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      "node.kubernetes.io/instance-type",
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_D2s_v3"},
				}})
			np.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
				Name: nodeClass.Name,
			}

			ExpectApplied(ctx, env.Client, np, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(128)))
			Expect(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings).To(BeNil())
		})
	})

	Context("Nodepool with KubeletConfig", func() {
		It("should support provisioning with kubeletConfig, computeResources and maxPods not specified", func() {
			nodeClass.Spec.Kubelet = &v1alpha2.KubeletConfiguration{
				ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
				ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
				CPUCFSQuota:                 lo.ToPtr(true),
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			customData := ExpectDecodedCustomData(azureEnv)

			expectedFlags := map[string]string{
				"eviction-hard":           "memory.available<750Mi",
				"image-gc-high-threshold": "30",
				"image-gc-low-threshold":  "20",
				"cpu-cfs-quota":           "true",
				"max-pods":                "250",
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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			customData := ExpectDecodedCustomData(azureEnv)
			// Since the network plugin is not "azure" it should not include the following kubeletLabels
			Expect(customData).To(Not(SatisfyAny(
				ContainSubstring("kubernetes.azure.com/network-subnet=karpentersub"),
				ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=test-vnet-guid"),
				ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
			)))
		})
		It("should support provisioning with kubeletConfig, computeResources and maxPods not specified", func() {
			nodeClass.Spec.Kubelet = &v1alpha2.KubeletConfiguration{
				ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
				ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
				CPUCFSQuota:                 lo.ToPtr(true),
			}

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			customData := ExpectDecodedCustomData(azureEnv)
			expectedFlags := map[string]string{
				"eviction-hard":           "memory.available<750Mi",
				"max-pods":                "250",
				"image-gc-low-threshold":  "20",
				"image-gc-high-threshold": "30",
				"cpu-cfs-quota":           "true",
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
			nodeClass.Spec.Kubelet = &v1alpha2.KubeletConfiguration{
				ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
				ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
				CPUCFSQuota:                 lo.ToPtr(true),
			}
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(15))

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			customData := ExpectDecodedCustomData(azureEnv)
			expectedFlags := map[string]string{
				"eviction-hard":           "memory.available<750Mi",
				"max-pods":                "15",
				"image-gc-low-threshold":  "20",
				"image-gc-high-threshold": "30",
				"cpu-cfs-quota":           "true",
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

	Context("Unavailable Offerings", func() {
		It("should not allocate a vm in a zone marked as unavailable", func() {
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
			Expect(node.Labels[v1alpha2.AlternativeLabelTopologyZone]).ToNot(Equal(fakeZone1))
			Expect(node.Labels[v1.LabelInstanceTypeStable]).To(Equal("Standard_D2_v2"))
		})
		It("should handle ZonalAllocationFailed on creating the VM", func() {
			azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
				&azcore.ResponseError{ErrorCode: sdkerrors.ZoneAllocationFailed},
			)
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
			zone, err := utils.GetZone(&azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM)
			Expect(err).ToNot(HaveOccurred())
			Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable("Standard_D2_v2", zone, karpv1.CapacityTypeSpot)).To(BeTrue())
			Expect(azureEnv.UnavailableOfferingsCache.IsUnavailable("Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)).To(BeTrue())

			By("successfully scheduling in a different zone on retry")
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels[v1alpha2.AlternativeLabelTopologyZone]).ToNot(Equal(zone))
		})

		DescribeTable("Should not return unavailable offerings", func(azEnv *test.Environment) {
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
			}
			instanceTypes, err := azEnv.InstanceTypesProvider.List(ctx, nodeClass)
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
		},
			Entry("zonal", azureEnv),
			Entry("non-zonal", azureEnvNonZonal),
		)

		It("should launch instances in a different zone than preferred", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fakeZone1, karpv1.CapacityTypeSpot)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
			})
			pod.Spec.Affinity = &v1.Affinity{NodeAffinity: &v1.NodeAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
				{
					Weight: 1, Preference: v1.NodeSelectorTerm{MatchExpressions: []v1.NodeSelectorRequirement{
						{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{fakeZone1}},
					}},
				},
			}}}
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels[v1alpha2.AlternativeLabelTopologyZone]).ToNot(Equal(fakeZone1))
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
		DescribeTable("should launch instances on later reconciliation attempt with Insufficient Capacity Error Cache expiry",
			func(azureEnv *test.Environment, cluster *state.Cluster, cloudProvider *cloudprovider.CloudProvider, coreProvisioner *provisioning.Provisioner) {
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
			},
			Entry("zonal", azureEnv, cluster, cloudProvider, coreProvisioner),
			Entry("non-zonal", azureEnvNonZonal, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal),
		)

		Context("SkuNotAvailable", func() {
			AssertUnavailable := func(sku string, capacityType string) {
				// fake a SKU not available error
				azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
					&azcore.ResponseError{ErrorCode: sdkerrors.SKUNotAvailableErrorCode},
				)
				coretest.ReplaceRequirements(nodePool,
					karpv1.NodeSelectorRequirementWithMinValues{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku}}},
					karpv1.NodeSelectorRequirementWithMinValues{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{capacityType}}},
				)
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				for _, zoneID := range []string{"1", "2", "3"} {
					ExpectUnavailable(azureEnv, sku, utils.MakeZone(fake.Region, zoneID), capacityType)
				}
			}

			It("should mark SKU as unavailable in all zones for Spot", func() {
				AssertUnavailable("Standard_D2_v2", karpv1.CapacityTypeSpot)
			})

			It("should mark SKU as unavailable in all zones for OnDemand", func() {
				AssertUnavailable("Standard_D2_v2", karpv1.CapacityTypeOnDemand)
			})
		})
	})
	Context("Provider List", func() {
		var instanceTypes corecloudprovider.InstanceTypes
		var err error

		BeforeEach(func() {
			// disable VM memory overhead for simpler capacity testing
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
				VMMemoryOverheadPercent: lo.ToPtr[float64](0),
			}))
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have all the requirements on every sku", func() {
			for _, instanceType := range instanceTypes {
				reqs := instanceType.Requirements

				Expect(reqs.Has(v1.LabelArchStable)).To(BeTrue())
				Expect(reqs.Has(v1.LabelOSStable)).To(BeTrue())
				Expect(reqs.Has(v1.LabelInstanceTypeStable)).To(BeTrue())

				Expect(reqs.Has(v1alpha2.LabelSKUName)).To(BeTrue())

				Expect(reqs.Has(v1alpha2.LabelSKUStoragePremiumCapable)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUEncryptionAtHostSupported)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUAcceleratedNetworking)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUHyperVGeneration)).To(BeTrue())
				Expect(reqs.Has(v1alpha2.LabelSKUStorageEphemeralOSMaxSize)).To(BeTrue())
			}
		})

		It("should have all compute capacity", func() {
			for _, instanceType := range instanceTypes {
				capList := instanceType.Capacity
				Expect(capList).To(HaveKey(v1.ResourceCPU))
				Expect(capList).To(HaveKey(v1.ResourceMemory))
				Expect(capList).To(HaveKey(v1.ResourcePods))
				Expect(capList).To(HaveKey(v1.ResourceEphemeralStorage))
			}
		})

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
				v1alpha2.LabelSKUName:                      "Standard_NC24ads_A100_v4",
				v1alpha2.LabelSKUFamily:                    "N",
				v1alpha2.LabelSKUVersion:                   "4",
				v1alpha2.LabelSKUStorageEphemeralOSMaxSize: "53.6870912",
				v1alpha2.LabelSKUAcceleratedNetworking:     "true",
				v1alpha2.LabelSKUEncryptionAtHostSupported: "true",
				v1alpha2.LabelSKUStoragePremiumCapable:     "true",
				v1alpha2.LabelSKUGPUName:                   "A100",
				v1alpha2.LabelSKUGPUManufacturer:           "nvidia",
				v1alpha2.LabelSKUGPUCount:                  "1",
				v1alpha2.LabelSKUCPU:                       "24",
				v1alpha2.LabelSKUMemory:                    "8192",
				v1alpha2.LabelSKUAccelerator:               "A100",
				// Deprecated Labels
				v1.LabelFailureDomainBetaRegion:    fake.Region,
				v1.LabelFailureDomainBetaZone:      fakeZone1,
				"beta.kubernetes.io/arch":          "amd64",
				"beta.kubernetes.io/os":            "linux",
				v1.LabelInstanceType:               "Standard_NC24ads_A100_v4",
				"topology.disk.csi.azure.com/zone": fakeZone1,
				v1.LabelWindowsBuild:               "window",
				// Cluster Label
				v1alpha2.AKSLabelCluster: "test-cluster",
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
		It("should propagate all values to requirements from skewer", func() {
			var gpuNode *corecloudprovider.InstanceType
			var normalNode *corecloudprovider.InstanceType
			for _, instanceType := range instanceTypes {
				if instanceType.Name == "Standard_D2_v2" {
					normalNode = instanceType
				}
				// #nosec G101
				if instanceType.Name == "Standard_NC24ads_A100_v4" {
					gpuNode = instanceType
				}
			}

			Expect(normalNode.Name).To(Equal("Standard_D2_v2"))
			Expect(gpuNode.Name).To(Equal("Standard_NC24ads_A100_v4"))

			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUName).Values()).To(ConsistOf("Standard_D2_v2"))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUName).Values()).To(ConsistOf("Standard_NC24ads_A100_v4"))

			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1alpha2.HyperVGenerationV1))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUHyperVGeneration).Values()).To(ConsistOf(v1alpha2.HyperVGenerationV2))

			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUAccelerator).Values()).To(ConsistOf("A100"))

			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUVersion).Values()).To(ConsistOf("2"))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUVersion).Values()).To(ConsistOf("4"))

			// CPU (requirements and capacity)
			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUCPU).Values()).To(ConsistOf("2"))
			Expect(normalNode.Capacity.Cpu().Value()).To(Equal(int64(2)))
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUCPU).Values()).To(ConsistOf("24"))
			Expect(gpuNode.Capacity.Cpu().Value()).To(Equal(int64(24)))

			// Memory (requirements and capacity)
			Expect(normalNode.Requirements.Get(v1alpha2.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(7 * 1024))) // 7GiB in MiB
			Expect(normalNode.Capacity.Memory().Value()).To(Equal(int64(7 * 1024 * 1024 * 1024)))                     // 7GiB in bytes
			Expect(gpuNode.Requirements.Get(v1alpha2.LabelSKUMemory).Values()).To(ConsistOf(fmt.Sprint(220 * 1024)))  // 220GiB in MiB
			Expect(gpuNode.Capacity.Memory().Value()).To(Equal(int64(220 * 1024 * 1024 * 1024)))                      // 220GiB in bytes

			// GPU -- Number of GPUs
			gpuQuantity, ok := gpuNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity")
			Expect(gpuQuantity.Value()).To(Equal(int64(1)))

			gpuQuantityNonGPU, ok := normalNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity, and be zero")
			Expect(gpuQuantityNonGPU.Value()).To(Equal(int64(0)))
		})
	})

	Context("ImageReference", func() {
		It("should use shared image gallery images when options are set to UseSIG", func() {
			options := test.Options(test.OptionsFields{
				UseSIG: lo.ToPtr(true),
			})
			ctx = options.ToContext(ctx)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// Expect virtual machine to have a shared image gallery id set on it
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
			Expect(vm.Properties.StorageProfile.ImageReference.ID).ShouldNot(BeNil())
			Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).Should(BeNil())

			Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(options.SIGSubscriptionID))
			Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring("AKSUbuntu"))
		})
		It("should use Community Images when options are set to UseSIG=false", func() {
			options := test.Options(test.OptionsFields{
				UseSIG: lo.ToPtr(false),
			})
			ctx = options.ToContext(ctx)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).Should(Not(BeNil()))

		})

	})
	Context("ImageProvider + Image Family", func() {
		DescribeTable("should select the right Shared Image Gallery image for a given instance type", func(instanceType string, imageFamily string, expectedImageDefinition string, expectedGalleryRG string, expectedGalleryURL string) {
			options := test.Options(test.OptionsFields{
				UseSIG: lo.ToPtr(true),
			})
			ctx = options.ToContext(ctx)
			nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{instanceType},
				}})
			nodePool.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{Name: nodeClass.Name}
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())

			expectedPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/galleries/%s/images/%s", options.SIGSubscriptionID, expectedGalleryRG, expectedGalleryURL, expectedImageDefinition)
			Expect(*vm.Properties.StorageProfile.ImageReference.ID).To(ContainSubstring(expectedPrefix))

		},

			Entry("Gen2, Gen1 instance type with AKSUbuntu image family", "Standard_D2_v5", v1alpha2.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
			Entry("Gen1 instance type with AKSUbuntu image family", "Standard_D2_v3", v1alpha2.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
			Entry("ARM instance type with AKSUbuntu image family", "Standard_D16plds_v5", v1alpha2.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuResourceGroup, imagefamily.AKSUbuntuGalleryName),
			Entry("Gen2 instance type with AzureLinux image family", "Standard_D2_v5", v1alpha2.AzureLinuxImageFamily, imagefamily.AzureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName),
			Entry("Gen1 instance type with AzureLinux image family", "Standard_D2_v3", v1alpha2.AzureLinuxImageFamily, imagefamily.AzureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName),
			Entry("ARM instance type with AzureLinux image family", "Standard_D16plds_v5", v1alpha2.AzureLinuxImageFamily, imagefamily.AzureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxResourceGroup, imagefamily.AKSAzureLinuxGalleryName),
		)
		DescribeTable("should select the right image for a given instance type",
			func(instanceType string, imageFamily string, expectedImageDefinition string, expectedGalleryURL string) {
				nodeClass.Spec.ImageFamily = lo.ToPtr(imageFamily)
				coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: v1.NodeSelectorRequirement{
						Key:      v1.LabelInstanceTypeStable,
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{instanceType},
					}})
				nodePool.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{Name: nodeClass.Name}
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				Expect(vm.Properties.StorageProfile.ImageReference).ToNot(BeNil())
				Expect(vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID).ToNot(BeNil())
				parts := strings.Split(*vm.Properties.StorageProfile.ImageReference.CommunityGalleryImageID, "/")
				Expect(parts[2]).To(Equal(expectedGalleryURL))
				Expect(parts[4]).To(Equal(expectedImageDefinition))

				// Need to reset env since we are doing these nested tests
				cluster.Reset()
				azureEnv.Reset()
			},
			Entry("Gen2, Gen1 instance type with AKSUbuntu image family",
				"Standard_D2_v5", v1alpha2.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
			Entry("Gen1 instance type with AKSUbuntu image family",
				"Standard_D2_v3", v1alpha2.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen1ImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
			Entry("ARM instance type with AKSUbuntu image family",
				"Standard_D16plds_v5", v1alpha2.Ubuntu2204ImageFamily, imagefamily.Ubuntu2204Gen2ArmImageDefinition, imagefamily.AKSUbuntuPublicGalleryURL),
			Entry("Gen2 instance type with AzureLinux image family",
				"Standard_D2_v5", v1alpha2.AzureLinuxImageFamily, imagefamily.AzureLinuxGen2ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
			Entry("Gen1 instance type with AzureLinux image family",
				"Standard_D2_v3", v1alpha2.AzureLinuxImageFamily, imagefamily.AzureLinuxGen1ImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
			Entry("ARM instance type with AzureLinux image family",
				"Standard_D16plds_v5", v1alpha2.AzureLinuxImageFamily, imagefamily.AzureLinuxGen2ArmImageDefinition, imagefamily.AKSAzureLinuxPublicGalleryURL),
		)
	})
	Context("Instance Types", func() {
		It("should support provisioning with no labels", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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

	Context("GPU Workloads + Nodes", func() {
		It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Properties).ToNot(BeNil())
			Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
			Expect(utils.IsNvidiaEnabledSKU(string(*vm.Properties.HardwareProfile.VMSize))).To(BeFalse())

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

			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)

			// the following checks assume Standard_NC16as_T4_v3 (surprisingly the cheapest GPU in the test set), so test the assumption
			Expect(node.Labels).To(HaveKeyWithValue("node.kubernetes.io/instance-type", "Standard_NC16as_T4_v3"))

			// Verify GPU related settings in bootstrap (assuming one Standard_NC16as_T4_v3)
			customData := ExpectDecodedCustomData(azureEnv)
			Expect(customData).To(SatisfyAll(
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

			// Verify that the node the pod was scheduled on has GPU resource and labels set
			Expect(node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-name", "T4"))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-manufacturer", v1alpha2.ManufacturerNvidia))
			Expect(node.Labels).To(HaveKeyWithValue("karpenter.azure.com/sku-gpu-count", "1"))
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
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
			customData = ExpectDecodedCustomData(azureEnv)
			kubeletFlags = ExpectKubeletFlagsPassed(customData)

			k8sVersion, err := azureEnv.ImageProvider.KubeServerVersion(ctx)
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
	Context("LoadBalancer", func() {
		resourceGroup := "test-resourceGroup"

		It("should include loadbalancer backend pools the allocated VMs", func() {
			standardLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.SLBName, true)
			internalLB := test.MakeStandardLoadBalancer(resourceGroup, loadbalancer.InternalSLBName, false)

			azureEnv.LoadBalancersAPI.LoadBalancers.Store(standardLB.ID, standardLB)
			azureEnv.LoadBalancersAPI.LoadBalancers.Store(internalLB.ID, internalLB)

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
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

	Context("Zone-aware provisioning", func() {
		It("should launch in the NodePool-requested zone", func() {
			zone, vmZone := "eastus-3", "3"
			nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: karpv1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{karpv1.CapacityTypeSpot, karpv1.CapacityTypeOnDemand}}},
				{NodeSelectorRequirement: v1.NodeSelectorRequirement{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{zone}}},
			}
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1alpha2.AlternativeLabelTopologyZone, zone))

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Zones).To(ConsistOf(&vmZone))
		})
		It("should support provisioning in non-zonal regions", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, clusterNonZonal, cloudProviderNonZonal, coreProvisionerNonZonal, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnvNonZonal.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnvNonZonal.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Zones).To(BeEmpty())
		})
		It("should support provisioning non-zonal instance types in zonal regions", func() {
			coretest.ReplaceRequirements(nodePool, karpv1.NodeSelectorRequirementWithMinValues{
				NodeSelectorRequirement: v1.NodeSelectorRequirement{
					Key:      v1.LabelInstanceTypeStable,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Standard_NC6s_v3"},
				}})
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)

			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels).To(HaveKeyWithValue(v1alpha2.AlternativeLabelTopologyZone, ""))

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Zones).To(BeEmpty())
		})
	})
})

var _ = Describe("Tax Calculator", func() {
	Context("KubeReservedResources", func() {
		It("should have 4 cores, 7GiB", func() {
			cpus := int64(4) // 4 cores
			memory := 7.0    // 7 GiB
			expectedCPU := "140m"
			expectedMemory := "1638Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})

		It("should have 2 cores, 8GiB", func() {
			cpus := int64(2) // 2 cores
			memory := 8.0    // 8 GiB
			expectedCPU := "100m"
			expectedMemory := "1843Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})

		It("should have 3 cores, 64GiB", func() {
			cpus := int64(3) // 3 cores
			memory := 64.0   // 64 GiB
			expectedCPU := "120m"
			expectedMemory := "5611Mi"

			resources := instancetype.KubeReservedResources(cpus, memory)
			gotCPU := resources[v1.ResourceCPU]
			gotMemory := resources[v1.ResourceMemory]

			Expect(gotCPU.String()).To(Equal(expectedCPU))
			Expect(gotMemory.String()).To(Equal(expectedMemory))
		})
	})
})

func createSDKErrorBody(code, message string) io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf(`{"error":{"code": "%s", "message": "%s"}}`, code, message))))
}

func ExpectKubeletFlagsPassed(customData string) string {
	GinkgoHelper()

	return customData[strings.Index(customData, "KUBELET_FLAGS=")+len("KUBELET_FLAGS=") : strings.Index(customData, "KUBELET_NODE_LABELS")]
}
