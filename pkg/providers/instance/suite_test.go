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

package instance_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/dynamicresources/deviceallocation"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	metrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	instancemetrics "github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/zones"
)

var ctx context.Context

var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var azureEnvNonZonal *test.Environment

var cloudProvider *cloudprovider.CloudProvider
var cloudProviderNonZonal *cloudprovider.CloudProvider

var fakeClock *clock.FakeClock
var cluster *state.Cluster
var coreProvisioner *provisioning.Provisioner

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)

	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))

	ctx, stop = context.WithCancel(ctx) //nolint:gosec // G118: stop is called in AfterSuite
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnv.InstanceTypeStore)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock, deviceallocation.NewController(env.Client))
	RunSpecs(t, "Provider/Azure")
}

func TestErrorCodeForMetrics(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error returns unknown",
			err:  nil,
			want: "UnknownError",
		},
		{
			name: "azure error with code",
			err:  &azcore.ResponseError{ErrorCode: "OperationNotAllowed"},
			want: "OperationNotAllowed",
		},
		{
			name: "azure error without code",
			err:  &azcore.ResponseError{StatusCode: http.StatusInternalServerError},
			want: "UnknownError",
		},
		{
			name: "generic error returns unknown",
			err:  errors.New("boom"),
			want: "UnknownError",
		},
	}

	for _, tc := range testCases {
		// capture range variable

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := instancemetrics.ErrorCodeForMetrics(tc.err)
			if got != tc.want {
				t.Fatalf("ErrorCodeForMetrics(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

func vmMetricLabelsFromCreateInput(input *fake.VirtualMachineCreateOrUpdateInput, nodePoolName string) map[string]string {
	labels := map[string]string{
		metrics.NodePoolLabel: nodePoolName,
	}
	if input == nil {
		return labels
	}
	return lo.Assign(vmMetricLabelsFromVM(&input.VM), labels)
}

func vmMetricLabelsFromVM(vm *armcompute.VirtualMachine) map[string]string {
	return map[string]string{
		metrics.ImageLabel:        imageIDFromVM(vm),
		metrics.SizeLabel:         vmSizeFromVM(vm),
		metrics.ZoneLabel:         zoneFromVM(vm),
		metrics.CapacityTypeLabel: instancemetrics.GetCapacityTypeFromVM(vm),
	}
}

func imageIDFromVM(vm *armcompute.VirtualMachine) string {
	if vm == nil || vm.Properties == nil || vm.Properties.StorageProfile == nil || vm.Properties.StorageProfile.ImageReference == nil {
		return ""
	}
	ref := vm.Properties.StorageProfile.ImageReference
	return lo.CoalesceOrEmpty(
		lo.FromPtr(ref.ID),
		lo.FromPtr(ref.CommunityGalleryImageID),
		lo.FromPtr(ref.SharedGalleryImageID),
		lo.FromPtr(ref.ExactVersion),
	)
}

func vmSizeFromVM(vm *armcompute.VirtualMachine) string {
	if vm == nil || vm.Properties == nil || vm.Properties.HardwareProfile == nil || vm.Properties.HardwareProfile.VMSize == nil {
		return ""
	}
	return string(*vm.Properties.HardwareProfile.VMSize)
}

func zoneFromVM(vm *armcompute.VirtualMachine) string {
	zone, _ := zones.MakeAKSLabelZoneFromVM(vm)
	return zone
}

// Attention: tests like below for AKSMachineInstanceProvider are added to cloudprovider module to reflect its end-to-end nature.
// Suggestion: move these tests there too(?)
var _ = Describe("VMInstanceProvider", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var nodePool *karpv1.NodePool
	var nodeClaim *karpv1.NodeClaim
	testOptions := options.FromContext(ctx)

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		test.ApplyDefaultStatus(nodeClass, env, testOptions.UseSIG)

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
				Labels: map[string]string{
					karpv1.NodePoolLabelKey: nodePool.Name,
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

		azureEnv.Reset(ctx)
		azureEnvNonZonal.Reset(ctx)
		cluster.Reset()
	})

	var _ = AfterEach(func() {
		cloudProvider.WaitForInstancePromises()
		ExpectCleanedUp(ctx, env.Client)
	})

	ZonalAndNonZonalRegions := []TableEntry{
		Entry("zonal", azureEnv, cloudProvider),
		Entry("non-zonal", azureEnvNonZonal, cloudProviderNonZonal),
	}

	Context("metrics integration", func() {
		BeforeEach(func() {
			instancemetrics.VMCreateStartMetric.Reset()
			instancemetrics.VMCreateFailureMetric.Reset()
		})

		It("records VM create start metric during successful launch", func() {
			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
			createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			labels := vmMetricLabelsFromCreateInput(createInput, nodePool.Name)

			metric, err := metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_start_total", labels)
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).NotTo(BeNil())
			Expect(metric.GetCounter().GetValue()).To(BeNumerically("==", 1))

			metric, err = metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_failure_total", metrics.FailureMetricLabels(labels, "sync"))
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).To(BeNil())

			metric, err = metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_failure_total", metrics.FailureMetricLabels(labels, "async"))
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).To(BeNil())
		})

		It("records VM create sync failure metric when Azure returns an error", func() {
			beginErr := &azcore.ResponseError{ErrorCode: "OperationNotAllowed"}
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.BeginError.Set(beginErr)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectNotScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
			createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			labels := vmMetricLabelsFromCreateInput(createInput, nodePool.Name)

			metric, err := metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_start_total", labels)
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).NotTo(BeNil())
			Expect(metric.GetCounter().GetValue()).To(BeNumerically("==", 1))

			syncFailureLabels := metrics.FailureMetricLabels(labels, "sync", map[string]string{metrics.ErrorCodeLabel: beginErr.ErrorCode})
			metric, err = metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_failure_total", syncFailureLabels)
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).NotTo(BeNil())
			Expect(metric.GetCounter().GetValue()).To(BeNumerically("==", 1))

			metric, err = metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_failure_total", metrics.FailureMetricLabels(labels, "async"))
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).To(BeNil())
		})

		It("records VM create async failure metric when provisioning poller fails", func() {
			pollerErr := &azcore.ResponseError{ErrorCode: "InternalOperationError"}
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.Error.Set(pollerErr)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(BeNumerically(">=", 1))
			createInput := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			labels := vmMetricLabelsFromCreateInput(createInput, nodePool.Name)

			metric, err := metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_start_total", labels)
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).NotTo(BeNil())
			Expect(metric.GetCounter().GetValue()).To(BeNumerically("==", 1))

			metric, err = metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_failure_total", metrics.FailureMetricLabels(labels, "sync"))
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).To(BeNil())

			asyncFailureLabels := metrics.FailureMetricLabels(labels, "async", map[string]string{metrics.ErrorCodeLabel: pollerErr.ErrorCode})
			metric, err = metrics.FindMetricWithLabelValues("karpenter_instance_vm_create_failure_total", asyncFailureLabels)
			Expect(err).NotTo(HaveOccurred())
			Expect(metric).NotTo(BeNil())
			Expect(metric.GetCounter().GetValue()).To(BeNumerically("==", 1))
		})
	})

	DescribeTable("should return an ICE error when all attempted instance types return an ICE error",
		func(azEnv *test.Environment, cp *cloudprovider.CloudProvider) {
			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zone, karpv1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zone, karpv1.CapacityTypeOnDemand)
			}
			if azEnv == azureEnv {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zones.Regional, karpv1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", fake.MakeSKU("Standard_D2_v2"), zones.Regional, karpv1.CapacityTypeOnDemand)
			}
			instanceTypes, err := cp.GetInstanceTypes(ctx, nodePool)
			Expect(err).ToNot(HaveOccurred())

			// Filter down to a single instance type
			instanceTypes = lo.Filter(instanceTypes, func(i *corecloudprovider.InstanceType, _ int) bool { return i.Name == "Standard_D2_v2" })

			// Since all the offerings are unavailable, this should return back an ICE error
			instance, err := azEnv.VMInstanceProvider.BeginCreate(ctx, nodeClass, nodeClaim, instanceTypes)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(instance).To(BeNil())
		},
		ZonalAndNonZonalRegions,
	)

	When("getting the auxiliary token", func() {
		var originalOptions *options.Options
		var originalEnv *test.Environment
		var originalCloudProvider *cloudprovider.CloudProvider
		newOptions := test.Options(test.OptionsFields{
			UseSIG: lo.ToPtr(true),
		})
		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			originalEnv = azureEnv
			originalCloudProvider = cloudProvider
			ctx = options.ToContext(
				ctx,
				newOptions)
			azureEnv = test.NewEnvironment(ctx, env)
			cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider,
				azureEnv.VMInstanceProvider,
				azureEnv.AKSMachineProvider,
				events.NewRecorder(&record.FakeRecorder{}),
				env.Client,
				azureEnv.ImageProvider,
				azureEnv.InstanceTypeStore,
			)
			test.ApplyDefaultStatus(nodeClass, env, newOptions.UseSIG)
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
			azureEnv = originalEnv
			cloudProvider = originalCloudProvider
			test.ApplyDefaultStatus(nodeClass, env, originalOptions.UseSIG)
		})
		Context("the token is not cached", func() {
			It("should get a new auxiliary token", func() {
				// first call using vm client should get token
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1)) // init token
			})
		})

		Context("token is cached by previous vmClient call", func() {
			BeforeEach(func() {
				_ = azureEnv.VirtualMachinesAPI.UseAuxiliaryTokenPolicy()
			})
			It("should use cached auxiliary token when still valid", func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(1)) // init token
				Expect(azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token).ToNot(BeNil())
			})

			It("should refresh auxiliary token if about to expire", func() {
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token.ExpiresOn = time.Now().Add(4 * time.Minute)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(2)) // init + refresh token
			})

			It("should refresh auxiliary token if after RefreshOn", func() {
				azureEnv.VirtualMachinesAPI.AuxiliaryTokenPolicy.Token.RefreshOn = time.Now().Add(-1 * time.Second)
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)

				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				Expect(azureEnv.AuxiliaryTokenServer.AuxiliaryTokenDoBehavior.CalledWithInput.Len()).To(Equal(2)) // init + refresh token
			})
		})
	})

	It("should list nic from karpenter provisioning request", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)
		interfaces, err := azureEnv.VMInstanceProvider.ListNics(ctx)
		Expect(err).To(BeNil())
		Expect(len(interfaces)).To(Equal(1))
	})
	It("should only list nics that belong to karpenter", func() {
		managedNic := test.Interface(test.InterfaceOptions{NodepoolName: nodePool.Name})
		unmanagedNic := test.Interface(test.InterfaceOptions{Tags: map[string]*string{"kubernetes.io/cluster/test-cluster": lo.ToPtr("random-aks-vm")}})

		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(unmanagedNic.ID), *unmanagedNic)
		interfaces, err := azureEnv.VMInstanceProvider.ListNics(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(interfaces)).To(Equal(1))
		Expect(interfaces[0].Name).To(Equal(managedNic.Name))
	})

	Context("Update", func() {
		It("should update only VM when no tags are included", func() {
			// Ensure that the VM already exists in the fake environment
			vmName := nodeClaim.Name
			vm := armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}

			azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)

			// Update the VM identities
			err := azureEnv.VMInstanceProvider.Update(ctx, vmName, armcompute.VirtualMachineUpdate{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.ManagedIdentity/userAssignedIdentities/aks-agentpool-00000000-identity": {},
					},
				},
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			update := azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.CalledWithInput.Pop().Updates
			Expect(update).ToNot(BeNil())
			Expect(update.Identity).ToNot(BeNil())
			Expect(update.Identity.UserAssignedIdentities).To(HaveLen(1))

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesUpdateTagsBehavior.CalledWithInput.Len()).To(Equal(0))
		})

		It("should update only VM, NIC, and Extensions when tags are included", func() {
			// Ensure that the VM already exists in the fake environment
			vmName := nodeClaim.Name
			vm := armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			// Ensure that the NIC already exists in the fake environment
			azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)
			nic := armnetwork.Interface{
				ID:   lo.ToPtr(fake.MakeNetworkInterfaceID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(*nic.ID, nic)

			// Ensure that the two VM extensions already exist in the fake environment
			billingExt := armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "computeAksLinuxBilling")),
				Name: lo.ToPtr("computeAksLinuxBilling"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			cseExt := armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "cse-agent-karpenter")),
				Name: lo.ToPtr("cse-agent-karpenter"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(*billingExt.ID, billingExt)
			azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(*cseExt.ID, cseExt)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)

			// Update the VM tags
			err := azureEnv.VMInstanceProvider.Update(ctx, vmName, armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
					"test-tag":                    lo.ToPtr("test-value"),
				},
			})
			Expect(err).ToNot(HaveOccurred())

			ExpectInstanceResourcesHaveTags(ctx, vmName, azureEnv, map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				"test-tag":                    lo.ToPtr("test-value"),
			})
		})

		It("should ignore NotFound errors for computeAksLinuxBilling extension update", func() {
			// Ensure that the VM already exists in the fake environment
			vmName := nodeClaim.Name
			vm := armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			// Ensure that the NIC already exists in the fake environment
			azureEnv.VirtualMachinesAPI.Instances.Store(*vm.ID, vm)
			nic := armnetwork.Interface{
				ID:   lo.ToPtr(fake.MakeNetworkInterfaceID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(*nic.ID, nic)

			// Ensure that only one extension exists in the env
			cseExt := armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "cse-agent-karpenter")),
				Name: lo.ToPtr("cse-agent-karpenter"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				},
			}
			azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(*cseExt.ID, cseExt)
			// TODO: This only works because this extension happens to be first in the list of extensions. If it were second it wouldn't work
			azureEnv.VirtualMachineExtensionsAPI.VirtualMachineExtensionsUpdateBehavior.BeginError.Set(&azcore.ResponseError{StatusCode: http.StatusNotFound}, fake.MaxCalls(1))

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)

			// Update the VM tags
			err := azureEnv.VMInstanceProvider.Update(ctx, vmName, armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
					"test-tag":                    lo.ToPtr("test-value"),
				},
			})
			Expect(err).ToNot(HaveOccurred())

			ExpectInstanceResourcesHaveTags(ctx, vmName, azureEnv, map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
				"test-tag":                    lo.ToPtr("test-value"),
			})
		})
	})

	Context("stale load balancer cache recovery", func() {
		It("should retry NIC creation after refreshing a stale backend pool reference", func() {
			// Seed the fake with a standard and internal LB
			nodeResourceGroup := options.FromContext(ctx).NodeResourceGroup
			standardLB := test.MakeStandardLoadBalancer(nodeResourceGroup, "kubernetes", true)
			internalLB := test.MakeStandardLoadBalancer(nodeResourceGroup, "kubernetes-internal", false)
			azureEnv.LoadBalancersAPI.LoadBalancers.Store(lo.FromPtr(standardLB.ID), standardLB)
			azureEnv.LoadBalancersAPI.LoadBalancers.Store(lo.FromPtr(internalLB.ID), internalLB)

			// Warm the LB cache so the provider has a generation-1 snapshot
			_, err := azureEnv.LoadBalancerProvider.LoadBalancerBackendPools(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Delete the internal LB from the fake (simulates Azure-side deletion while cache is warm)
			azureEnv.LoadBalancersAPI.LoadBalancers.Delete(lo.FromPtr(internalLB.ID))

			// Make the first NIC creation fail with InvalidResourceReference for the now-deleted pool,
			// then succeed on retry (after the LB cache is refreshed).
			deletedPoolID := fake.MakeBackendAddressPoolID(nodeResourceGroup, "kubernetes-internal", "kubernetes")
			nicCreateCalls := 0
			azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.SetCustomTransformer(
				func(input *fake.NetworkInterfaceCreateOrUpdateInput) error {
					nicCreateCalls++
					if nicCreateCalls == 1 {
						return fmt.Errorf(
							"Resource %s referenced by resource /subscriptions/test/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s was not found. "+
								"Please make sure that the referenced resource exists, and that both resources are in the same region.: %w",
							deletedPoolID,
							nodeResourceGroup,
							input.InterfaceName,
							&azcore.ResponseError{ErrorCode: "InvalidResourceReference", StatusCode: 400},
						)
					}
					return nil
				},
			)

			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			// NIC creation should have been called twice: first fails, refresh, second succeeds
			Expect(nicCreateCalls).To(Equal(2))

			// The second NIC request should NOT include the deleted pool
			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(2))
			// Pop returns most recent first (stack order)
			secondNIC := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()
			firstNIC := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop()

			// First attempt included the deleted pool
			firstPools := firstNIC.Interface.Properties.IPConfigurations[0].Properties.LoadBalancerBackendAddressPools
			firstPoolIDs := lo.Map(firstPools, func(p *armnetwork.BackendAddressPool, _ int) string { return lo.FromPtr(p.ID) })
			Expect(firstPoolIDs).To(ContainElement(deletedPoolID))

			// Second attempt should not include the deleted pool
			secondPools := secondNIC.Interface.Properties.IPConfigurations[0].Properties.LoadBalancerBackendAddressPools
			secondPoolIDs := lo.Map(secondPools, func(p *armnetwork.BackendAddressPool, _ int) string { return lo.FromPtr(p.ID) })
			Expect(secondPoolIDs).ToNot(ContainElement(deletedPoolID))
		})
	})
})
