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
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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

	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"

	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/controllers/provisioning"
	"github.com/aws/karpenter-core/pkg/controllers/state"
	"github.com/aws/karpenter-core/pkg/events"
	"github.com/aws/karpenter-core/pkg/operator/scheme"
	coretest "github.com/aws/karpenter-core/pkg/test"
	. "github.com/aws/karpenter-core/pkg/test/expectations"

	sdkerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter/pkg/apis"
	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/instancetype"
	"github.com/Azure/karpenter/pkg/providers/loadbalancer"
	"github.com/Azure/karpenter/pkg/test"
	. "github.com/Azure/karpenter/pkg/test/expectations"
	"github.com/Azure/karpenter/pkg/utils"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv, azureEnvNonZonal *test.Environment
var fakeClock *clock.FakeClock
var coreProvisioner, coreProvisionerNonZonal *provisioning.Provisioner
var cluster, clusterNonZonal *state.Cluster
var cloudProvider, cloudProviderNonZonal *cloudprovider.CloudProvider

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = settings.ToContext(ctx, test.Settings())

	env = coretest.NewEnvironment(scheme.Scheme, coretest.WithCRDs(apis.CRDs...))

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)

	fakeClock = &clock.FakeClock{}
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)

	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	clusterNonZonal = state.NewCluster(fakeClock, env.Client, cloudProviderNonZonal)
	coreProvisioner = provisioning.NewProvisioner(env.Client, env.KubernetesInterface.CoreV1(), events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster)
	coreProvisionerNonZonal = provisioning.NewProvisioner(env.Client, env.KubernetesInterface.CoreV1(), events.NewRecorder(&record.FakeRecorder{}), cloudProviderNonZonal, clusterNonZonal)

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
	var nodePool *corev1beta1.NodePool

	BeforeEach(func() {
		os.Setenv("AZURE_VNET_GUID", "test-vnet-guid")
		os.Setenv("AZURE_VNET_NAME", "aks-vnet-00000000")
		os.Setenv("AZURE_SUBNET_NAME", "test-subnet-name")

		nodeClass = test.AKSNodeClass()
		// Sometimes we use nodeClass without applying it, when simulating the List() call.
		// In that case, we need to set the default values for the node class.
		nodeClass.Spec.OSDiskSizeGB = lo.ToPtr[int32](128)
		nodePool = coretest.NodePool(corev1beta1.NodePool{
			Spec: corev1beta1.NodePoolSpec{
				Template: corev1beta1.NodeClaimTemplate{
					Spec: corev1beta1.NodeClaimSpec{
						NodeClassRef: &corev1beta1.NodeClassReference{
							Name: nodeClass.Name,
						},
					},
				},
			},
		})

		cluster.Reset()
		clusterNonZonal.Reset()
		azureEnv.Reset()
		azureEnvNonZonal.Reset()
	})

	AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	Context("vm creation error responses", func() {
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
			nodeClaim := coretest.NodeClaim(corev1beta1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						corev1beta1.NodePoolLabelKey: nodePool.Name,
					},
				},
				Spec: corev1beta1.NodeClaimSpec{
					NodeClassRef: &corev1beta1.NodeClassReference{
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
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, nodeClass)
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

	})

	Context("Ephemeral Disk", func() {
		It("should use ephemeral disk if supported, and has space of at least 128GB by default", func() {
			// Create a Provisioner that selects a sku that supports ephemeral
			// SKU Standard_D64s_v3 has 1600GB of CacheDisk space, so we expect we can create an ephemeral disk with size 128GB

			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, v1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"},
			})
			np.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
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
			// Create a Provisioner that selects a sku that supports ephemeral
			// SKU Standard_D64s_v3 has 1600GB of CacheDisk space, so we expect we can create an ephemeral disk with size 256GB
			provider := test.AKSNodeClass()
			provider.Spec.OSDiskSizeGB = lo.ToPtr[int32](256)
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, v1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D64s_v3"},
			})
			np.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
				Name: provider.Name,
			}

			ExpectApplied(ctx, env.Client, np, provider)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm).NotTo(BeNil())
			Expect(vm.Properties.StorageProfile.OSDisk.DiskSizeGB).NotTo(BeNil())
			Expect(*vm.Properties.StorageProfile.OSDisk.DiskSizeGB).To(Equal(int32(256)))
			Expect(lo.FromPtr(vm.Properties.StorageProfile.OSDisk.DiffDiskSettings.Option)).To(Equal(armcompute.DiffDiskOptionsLocal))
		})
		It("if ephemeral is supported, but we don't have enough space, we should not use ephemeral disk", func() {
			// Create a Provisioner that selects a sku that supports ephemeral Standard_D2s_v3
			// Standard_D2s_V3 has 53GB Of CacheDisk space,
			// and has 16GB of Temp Disk Space.
			// With our rule of 100GB being the minimum OSDiskSize, this VM should be created without local disk
			np := coretest.NodePool()
			np.Spec.Template.Spec.Requirements = append(np.Spec.Template.Spec.Requirements, v1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2s_v3"},
			})
			np.Spec.Template.Spec.NodeClassRef = &corev1beta1.NodeClassReference{
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

	Context("Provisioner with KubeletConfig", func() {
		kubeletConfig := &corev1beta1.KubeletConfiguration{
			PodsPerCore: lo.ToPtr(int32(110)),
			EvictionSoft: map[string]string{
				instancetype.MemoryAvailable: "1Gi",
			},
			EvictionSoftGracePeriod: map[string]metav1.Duration{
				instancetype.MemoryAvailable: {Duration: 10 * time.Second},
			},
			EvictionMaxPodGracePeriod:   lo.ToPtr(int32(15)),
			ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
			ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
			CPUCFSQuota:                 lo.ToPtr(true),
		}

		It("should support provisioning with kubeletConfig, computeResources & maxPods not specified", func() {

			nodePool.Spec.Template.Spec.Kubelet = kubeletConfig
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			customData := *vm.Properties.OSProfile.CustomData
			Expect(customData).ToNot(BeNil())
			decodedBytes, err := base64.StdEncoding.DecodeString(customData)
			Expect(err).To(Succeed())
			decodedString := string(decodedBytes[:])
			kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--system-reserved=cpu=0,memory=0"),
				ContainSubstring("--system-reserved=memory=0,cpu=0"),
			))
			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
				ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
			))

			Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
			Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
			Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
			Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
		})

		It("should support provisioning with kubeletConfig, computeResources and maxPods specified", func() {
			kubeletConfig.SystemReserved = v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("200m"),
				v1.ResourceMemory: resource.MustParse("1Gi"),
			}
			kubeletConfig.KubeReserved = v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("100m"),
				v1.ResourceMemory: resource.MustParse("500Mi"),
			}
			kubeletConfig.EvictionHard = map[string]string{
				instancetype.MemoryAvailable: "10Mi",
			}
			kubeletConfig.MaxPods = lo.ToPtr(int32(15))

			nodePool.Spec.Template.Spec.Kubelet = kubeletConfig

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			customData := *vm.Properties.OSProfile.CustomData
			Expect(customData).ToNot(BeNil())
			decodedBytes, err := base64.StdEncoding.DecodeString(customData)
			Expect(err).To(Succeed())
			decodedString := string(decodedBytes[:])
			kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--system-reserved=cpu=0,memory=0"),
				ContainSubstring("--system-reserved=memory=0,cpu=0"),
			))
			Expect(kubeletFlags).To(SatisfyAny(
				ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
				ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
			))

			Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
			Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
			Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
			Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
			Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
			Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
		})
	})

	Context("Provisioner with VnetNodeLabel", func() {
		It("should support provisioning with Vnet node labels", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod()
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			customData := *vm.Properties.OSProfile.CustomData
			Expect(customData).ToNot(BeNil())
			decodedBytes, err := base64.StdEncoding.DecodeString(customData)
			Expect(err).To(Succeed())
			decodedString := string(decodedBytes[:])
			Expect(decodedString).To(SatisfyAll(
				ContainSubstring("kubernetes.azure.com/ebpf-dataplane=cilium"),
				ContainSubstring("kubernetes.azure.com/network-name=aks-vnet-00000000"),
				ContainSubstring("kubernetes.azure.com/network-subnet=test-subnet-name"),
				ContainSubstring("kubernetes.azure.com/network-subscription=test-subscription"),
				ContainSubstring("kubernetes.azure.com/nodenetwork-vnetguid=test-vnet-guid"),
				ContainSubstring("kubernetes.azure.com/podnetwork-type=overlay"),
			))
		})
	})

	Context("Unavailable Offerings", func() {
		It("should not allocate a vm in a zone marked as unavailable", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), corev1beta1.CapacityTypeSpot)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), corev1beta1.CapacityTypeOnDemand)
			coretest.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_D2_v2"},
			})

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			// Try this 100 times to make sure we don't get a node in eastus-1,
			// we pick from 3 zones so the likelihood of this test passing by chance is 1/3^100
			for i := 0; i < 100; i++ {
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)
				nodes := &v1.NodeList{}
				Expect(env.Client.List(ctx, nodes)).To(Succeed())
				for _, node := range nodes.Items {
					Expect(node.Labels["karpenter.k8s.azure/zone"]).ToNot(Equal(fmt.Sprintf("%s-1", fake.Region)))
					Expect(node.Labels["node.kubernetes.io/instance-type"]).To(Equal("Standard_D2_v2"))

				}
			}

		})

		DescribeTable("Should not return unavailable offerings", func(azEnv *test.Environment) {
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, corev1beta1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, corev1beta1.CapacityTypeOnDemand)
			}
			instanceTypes, err := azEnv.InstanceTypesProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, nodeClass)
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
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "ZonalAllocationFailure", "Standard_D2_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeSpot)

			ExpectApplied(ctx, env.Client, nodeClass, nodePool)
			pod := coretest.UnschedulablePod(coretest.PodOptions{
				NodeSelector: map[string]string{v1.LabelInstanceTypeStable: "Standard_D2_v2"},
			})
			pod.Spec.Affinity = &v1.Affinity{NodeAffinity: &v1.NodeAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []v1.PreferredSchedulingTerm{
				{
					Weight: 1, Preference: v1.NodeSelectorTerm{MatchExpressions: []v1.NodeSelectorRequirement{
						{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{fmt.Sprintf("%s-1", fake.Region)}},
					}},
				},
			}}}
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			node := ExpectScheduled(ctx, env.Client, pod)
			Expect(node.Labels["karpenter.k8s.azure/zone"]).ToNot(Equal(fmt.Sprintf("%s-1", fake.Region)))
			Expect(node.Labels["node.kubernetes.io/instance-type"]).To(Equal("Standard_D2_v2"))
		})
		It("should launch smaller instances than optimal if larger instance launch results in Insufficient Capacity Error", func() {
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeOnDemand)
			azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_F16s_v2", fmt.Sprintf("%s-1", fake.Region), v1alpha5.CapacityTypeSpot)
			coretest.ReplaceRequirements(nodePool, v1.NodeSelectorRequirement{
				Key:      v1.LabelInstanceTypeStable,
				Operator: v1.NodeSelectorOpIn,
				Values:   []string{"Standard_DS2_v2", "Standard_F16s_v2"},
			})
			pods := []*v1.Pod{}
			for i := 0; i < 2; i++ {
				pods = append(pods, coretest.UnschedulablePod(coretest.PodOptions{
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("1")},
					},
					NodeSelector: map[string]string{
						v1.LabelTopologyZone: fmt.Sprintf("%s-1", fake.Region),
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
					azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, v1alpha5.CapacityTypeSpot)
					azureEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, v1alpha5.CapacityTypeOnDemand)
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

		Context("on SkuNotUnavailable, should cache SKU as unavailable in all zones", func() {
			AssertUnavailable := func(sku string, capacityType string) {
				// fake a SKU not available error
				azureEnv.VirtualMachinesAPI.VirtualMachinesBehavior.VirtualMachineCreateOrUpdateBehavior.Error.Set(
					&azcore.ResponseError{ErrorCode: sdkerrors.SKUNotAvailableErrorCode},
				)
				coretest.ReplaceRequirements(nodePool,
					v1.NodeSelectorRequirement{Key: v1.LabelInstanceTypeStable, Operator: v1.NodeSelectorOpIn, Values: []string{sku}},
					v1.NodeSelectorRequirement{Key: corev1beta1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{capacityType}},
				)
				ExpectApplied(ctx, env.Client, nodeClass, nodePool)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectNotScheduled(ctx, env.Client, pod)
				for _, zone := range []string{"1", "2", "3"} {
					ExpectUnavailable(azureEnv, sku, zone, capacityType)
				}
			}

			It("should mark SKU as unavailable in all zones for Spot", func() {
				AssertUnavailable("Standard_D2_v2", corev1beta1.CapacityTypeSpot)
			})

			It("should mark SKU as unavailable in all zones for OnDemand", func() {
				AssertUnavailable("Standard_D2_v2", corev1beta1.CapacityTypeOnDemand)
			})
		})
	})
	Context("Provider List", func() {
		var instanceTypes corecloudprovider.InstanceTypes
		var err error

		BeforeEach(func() {
			// disable VM memory overhead for simpler capacity testing
			ctx = settings.ToContext(ctx, test.Settings(test.SettingOptions{
				VMMemoryOverheadPercent: lo.ToPtr[float64](0),
			}))
			instanceTypes, err = azureEnv.InstanceTypesProvider.List(ctx, &corev1beta1.KubeletConfiguration{}, nodeClass)
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

			gpuQuanityNonGPU, ok := normalNode.Capacity["nvidia.com/gpu"]
			Expect(ok).To(BeTrue(), "Expected nvidia.com/gpu to be present in capacity, and be zero")
			Expect(gpuQuanityNonGPU.Value()).To(Equal(int64(0)))
		})
	})

	Context("Instance Types", func() {
		It("should support provisioning with no labels", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)
		})
		Context("VM profile", func() {
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
		})

		It("should have VM identity set", func() {
			ctx = settings.ToContext(
				ctx,
				test.Settings(test.SettingOptions{
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
	})

	Context("GPU workloads and Nodes", func() {
		It("should schedule non-GPU pod onto the cheapest non-GPU capable node", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
			Expect(vm.Properties).ToNot(BeNil())
			Expect(vm.Properties.HardwareProfile).ToNot(BeNil())
			Expect(utils.IsNvidiaEnabledSKU(string(*vm.Properties.HardwareProfile.VMSize))).To(BeFalse())

			clusterNodes := cluster.Nodes()
			node := clusterNodes[0]
			if node.Name() == pod.Spec.NodeName {
				nodeLabels := node.GetLabels()
				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-count", "0"))
			}
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
			ExpectScheduled(ctx, env.Client, pod)

			// Verify that the node has the GPU label set that the pod was scheduled on
			clusterNodes := cluster.Nodes()
			Expect(clusterNodes).ToNot(BeEmpty())
			Expect(len(clusterNodes)).To(Equal(1))
			node := clusterNodes[0]
			Expect(node.Node.Status.Allocatable).To(HaveKeyWithValue(v1.ResourceName("nvidia.com/gpu"), resource.MustParse("1")))

			if node.Name() == pod.Spec.NodeName {
				nodeLabels := node.GetLabels()

				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-name", "A100"))
				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-manufacturer", v1alpha2.ManufacturerNvidia))
				Expect(nodeLabels).To(HaveKeyWithValue("karpenter.k8s.azure/sku-gpu-count", "1"))

			}
		})

		Context("Provisioner with KubeletConfig", func() {
			It("Should support provisioning with kubeletConfig, computeResources and maxPods not specified", func() {

				nodePool.Spec.Template.Spec.Kubelet = &corev1beta1.KubeletConfiguration{
					PodsPerCore: lo.ToPtr(int32(110)),
					EvictionSoft: map[string]string{
						instancetype.MemoryAvailable: "1Gi",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						instancetype.MemoryAvailable: {Duration: 10 * time.Second},
					},
					EvictionMaxPodGracePeriod:   lo.ToPtr(int32(15)),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
					CPUCFSQuota:                 lo.ToPtr(true),
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				customData := *vm.Properties.OSProfile.CustomData
				Expect(customData).ToNot(BeNil())
				decodedBytes, err := base64.StdEncoding.DecodeString(customData)
				Expect(err).To(Succeed())
				decodedString := string(decodedBytes[:])
				kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

				Expect(kubeletFlags).To(SatisfyAny( // AKS default
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(kubeletFlags).To(SatisfyAny( // AKS calculation based on cpu and memory
					ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
					ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
				))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi")) // AKS default
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
				Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
				Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
				Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
			})
			It("Should support provisioning with kubeletConfig, computeResources and maxPods specified", func() {

				nodePool.Spec.Template.Spec.Kubelet = &corev1beta1.KubeletConfiguration{
					PodsPerCore: lo.ToPtr(int32(110)),
					EvictionSoft: map[string]string{
						instancetype.MemoryAvailable: "1Gi",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						instancetype.MemoryAvailable: {Duration: 10 * time.Second},
					},
					EvictionMaxPodGracePeriod:   lo.ToPtr(int32(15)),
					ImageGCHighThresholdPercent: lo.ToPtr(int32(30)),
					ImageGCLowThresholdPercent:  lo.ToPtr(int32(20)),
					CPUCFSQuota:                 lo.ToPtr(true),

					SystemReserved: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("200m"),
						v1.ResourceMemory: resource.MustParse("1Gi"),
					},
					KubeReserved: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("100m"),
						v1.ResourceMemory: resource.MustParse("500Mi"),
					},
					EvictionHard: map[string]string{
						instancetype.MemoryAvailable: "10Mi",
					},
					MaxPods: lo.ToPtr(int32(15)),
				}

				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM
				customData := *vm.Properties.OSProfile.CustomData
				Expect(customData).ToNot(BeNil())
				decodedBytes, err := base64.StdEncoding.DecodeString(customData)
				Expect(err).To(Succeed())
				decodedString := string(decodedBytes[:])
				kubeletFlags := decodedString[strings.Index(decodedString, "KUBELET_FLAGS=")+len("KUBELET_FLAGS="):]

				Expect(kubeletFlags).To(SatisfyAny( // AKS default
					ContainSubstring("--system-reserved=cpu=0,memory=0"),
					ContainSubstring("--system-reserved=memory=0,cpu=0"),
				))
				Expect(kubeletFlags).To(SatisfyAny( // AKS calculation based on cpu and memory
					ContainSubstring("--kube-reserved=cpu=100m,memory=1843Mi"),
					ContainSubstring("--kube-reserved=memory=1843Mi,cpu=100m"),
				))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-hard=memory.available<750Mi")) // AKS default
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft=memory.available<1Gi"))
				Expect(kubeletFlags).To(ContainSubstring("--eviction-soft-grace-period=memory.available=10s"))
				Expect(kubeletFlags).To(ContainSubstring("--max-pods=100")) // kubenet
				Expect(kubeletFlags).To(ContainSubstring("--pods-per-core=110"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-low-threshold=20"))
				Expect(kubeletFlags).To(ContainSubstring("--image-gc-high-threshold=30"))
				Expect(kubeletFlags).To(ContainSubstring("--cpu-cfs-quota=true"))
			})
		})
	})

	Context("LoadBalancer backend pools", func() {
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

	Context("Zone aware provisioning", func() {
		It("should launch in the NodePool-requested zone", func() {
			zone, vmZone := "eastus-3", "3"
			nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirement{
				{Key: corev1beta1.CapacityTypeLabelKey, Operator: v1.NodeSelectorOpIn, Values: []string{corev1beta1.CapacityTypeSpot, corev1beta1.CapacityTypeOnDemand}},
				{Key: v1.LabelTopologyZone, Operator: v1.NodeSelectorOpIn, Values: []string{zone}},
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
	})

})

var _ = Describe("Tax Calculator", func() {
	Context("KubeReservedResources", func() {
		It("4 cores, 7GiB", func() {
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

		It("2 cores, 8GiB", func() {
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

		It("3 cores, 64GiB", func() {
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
