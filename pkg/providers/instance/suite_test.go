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
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
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
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	metrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	instancemetrics "github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
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

	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	azureEnvNonZonal = test.NewEnvironmentNonZonal(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.VMInstanceProvider, azureEnvNonZonal.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider, azureEnv.InstanceTypeStore)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)
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

		azureEnv.Reset()
		azureEnvNonZonal.Reset()
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

	Context("AzureCNI V1", func() {
		var originalOptions *options.Options

		BeforeEach(func() {
			originalOptions = options.FromContext(ctx)
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					NetworkPlugin:     lo.ToPtr(consts.NetworkPluginAzure),
					NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeNone),
				}))
		})

		AfterEach(func() {
			ctx = options.ToContext(ctx, originalOptions)
		})
		It("should include 30 secondary ips by default for NodeSubnet", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			Expect(len(nic.Properties.IPConfigurations)).To(Equal(30))
			customData := ExpectDecodedCustomData(azureEnv)
			expectedFlags := map[string]string{
				"max-pods": "30",
			}
			ExpectKubeletFlags(azureEnv, customData, expectedFlags)
		})
		It("should include 1 ip config for Azure CNI Overlay", func() {
			ctx = options.ToContext(
				ctx,
				test.Options(test.OptionsFields{
					NetworkPlugin:     lo.ToPtr(consts.NetworkPluginAzure),
					NetworkPluginMode: lo.ToPtr(consts.NetworkPluginModeOverlay),
				}))

			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			// Overlay doesn't rely on secondary ips and instead allocates from a
			// virtual address space.
			Expect(len(nic.Properties.IPConfigurations)).To(Equal(1))
			customData := ExpectDecodedCustomData(azureEnv)
			expectedFlags := map[string]string{
				"max-pods": "250",
			}
			ExpectKubeletFlags(azureEnv, customData, expectedFlags)
		})
		It("should set the number of secondary ips equal to max pods (NodeSubnet)", func() {
			nodeClass.Spec.MaxPods = lo.ToPtr(int32(11))
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			Expect(len(nic.Properties.IPConfigurations)).To(Equal(11))
		})
	})

	It("should create VM and NIC with valid ARM tags", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
		vm, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
		Expect(err).To(BeNil())
		tags := vm.Tags
		Expect(lo.FromPtr(tags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.FromPtr(tags[launchtemplate.BillingTagKey])).To(Equal("linux"))
		Expect(lo.PickBy(tags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		nicTags := nic.Tags
		Expect(lo.FromPtr(nicTags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.FromPtr(nicTags[launchtemplate.BillingTagKey])).To(Equal("linux"))
		Expect(lo.PickBy(nicTags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))
	})

	It("should not allow the user to override Karpenter-managed tags", func() {
		nodeClass.Spec.Tags = map[string]string{
			"karpenter.azure.com/cluster": "my-override-cluster",
			"karpenter.sh/nodepool":       "my-override-nodepool",
			"compute.aks.billing":         "my-override-billing",
		}
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
		vm, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
		Expect(err).To(BeNil())
		tags := vm.Tags
		Expect(lo.FromPtr(tags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.FromPtr(tags[launchtemplate.KarpenterManagedTagKey])).To(Equal(testOptions.ClusterName))
		Expect(lo.FromPtr(tags[launchtemplate.BillingTagKey])).To(Equal("linux"))

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		nicTags := nic.Tags
		Expect(lo.FromPtr(nicTags[launchtemplate.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.FromPtr(nicTags[launchtemplate.KarpenterManagedTagKey])).To(Equal(testOptions.ClusterName))
		Expect(lo.FromPtr(nicTags[launchtemplate.BillingTagKey])).To(Equal("linux"))
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

	It("should create VM with custom Linux admin username", func() {
		customUsername := "customuser"
		ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
			LinuxAdminUsername: lo.ToPtr(customUsername),
		}))

		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

		// Verify the custom username was propagated
		Expect(vm.Properties.OSProfile.AdminUsername).ToNot(BeNil())
		Expect(*vm.Properties.OSProfile.AdminUsername).To(Equal(customUsername))

		// Verify SSH key path uses the custom username
		Expect(vm.Properties.OSProfile.LinuxConfiguration).ToNot(BeNil())
		Expect(vm.Properties.OSProfile.LinuxConfiguration.SSH).ToNot(BeNil())
		Expect(vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys).To(HaveLen(1))
		expectedPath := "/home/" + customUsername + "/.ssh/authorized_keys"
		Expect(*vm.Properties.OSProfile.LinuxConfiguration.SSH.PublicKeys[0].Path).To(Equal(expectedPath))
	})

	It("should attach nsg to nic when in BYO VNET mode", func() {
		ctx = options.ToContext(
			ctx,
			test.Options(test.OptionsFields{
				SubnetID: lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet"), // different RG
			}))
		nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, "aks-agentpool-00000000-nsg")
		azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)

		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		expectedNSGID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/aks-agentpool-%s-nsg", azureEnv.SubscriptionID, options.FromContext(ctx).NodeResourceGroup, options.FromContext(ctx).ClusterID)
		Expect(nic.Properties.NetworkSecurityGroup).ToNot(BeNil())
		Expect(lo.FromPtr(nic.Properties.NetworkSecurityGroup.ID)).To(Equal(expectedNSGID))
	})

	It("should attach nsg to nic when NodeClass VNET specified", func() {
		nodeClass.Spec.VNETSubnetID = lo.ToPtr("/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/sillygeese/providers/Microsoft.Network/virtualNetworks/aks-vnet-12345678/subnets/aks-subnet") // different RG

		nsg := test.MakeNetworkSecurityGroup(options.FromContext(ctx).NodeResourceGroup, "aks-agentpool-00000000-nsg")
		azureEnv.NetworkSecurityGroupAPI.NSGs.Store(nsg.ID, nsg)

		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		expectedNSGID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/aks-agentpool-%s-nsg", azureEnv.SubscriptionID, options.FromContext(ctx).NodeResourceGroup, options.FromContext(ctx).ClusterID)
		Expect(nic.Properties.NetworkSecurityGroup).ToNot(BeNil())
		Expect(lo.FromPtr(nic.Properties.NetworkSecurityGroup.ID)).To(Equal(expectedNSGID))
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

	Context("EncryptionAtHost", func() {
		It("should create VM with EncryptionAtHost enabled when specified in AKSNodeClass", func() {
			if nodeClass.Spec.Security == nil {
				nodeClass.Spec.Security = &v1beta1.Security{}
			}
			nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(true)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

			Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
			Expect(vm.Properties.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
			Expect(lo.FromPtr(vm.Properties.SecurityProfile.EncryptionAtHost)).To(BeTrue())
		})

		It("should create VM with EncryptionAtHost disabled when specified in AKSNodeClass", func() {
			if nodeClass.Spec.Security == nil {
				nodeClass.Spec.Security = &v1beta1.Security{}
			}
			nodeClass.Spec.Security.EncryptionAtHost = lo.ToPtr(false)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

			Expect(vm.Properties.SecurityProfile).ToNot(BeNil())
			Expect(vm.Properties.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
			Expect(lo.FromPtr(vm.Properties.SecurityProfile.EncryptionAtHost)).To(BeFalse())
		})

		It("should create VM without SecurityProfile when EncryptionAtHost is not specified in AKSNodeClass", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisionedAndWaitForPromises(ctx, env.Client, cluster, cloudProvider, coreProvisioner, azureEnv, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vm := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VM

			Expect(vm.Properties.SecurityProfile).To(BeNil())
		})
	})

	Context("large-scale provisioning with quota exhaustion", func() {
		It("should fall back to a different SKU family when quota is exhausted mid-provisioning", func() {
			// Restrict NodePool to the Ds_v3 and D_v5 SKU series (standardDSv3Family and standardDv5Family).
			// The standardDSv3Family has D2s_v3 (2 vCPU), D4s_v3 (4 vCPU), D64s_v3 (64 vCPU).
			// With a 50-core quota: D64s_v3 exceeds quota immediately, D4s_v3 VMs succeed
			// until the quota fills (~12 VMs = 48 cores), then D2s_v3 squeezes in one
			// more, and eventually all Ds_v3 sizes are blocked. 50 pods × 1.5 vCPU = 75
			// vCPU of work cannot fit in a 50-core quota, so the remainder falls back to D_v5.
			coretest.ReplaceRequirements(nodePool,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelSKUSeries,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Ds_v3", "D_v5"},
				},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand},
				},
			)
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			// Create 50 pods, each requesting 1500m CPU
			const totalPods = 50
			const coreQuota = 50 // standardDSv3Family core quota limit
			pods := make([]*v1.Pod, totalPods)
			for i := range pods {
				pods[i] = coretest.UnschedulablePod(coretest.PodOptions{
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource.MustParse("1500m"),
						},
					},
				})
			}

			// Simulate quota exhaustion: track total cores consumed by VMs in the
			// standardDSv3Family. When the next VM would push total cores beyond the
			// quota, return a subscription quota error. As progressively smaller SKUs
			// fail, the error handler lowers the blocked-CPU threshold for the family
			// until all Ds_v3 sizes are unavailable. Remaining pods fall back to D_v5.
			var usedCores atomic.Int32
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.SetCustomTransformer(
				func(input *fake.VirtualMachineCreateOrUpdateInput) error {
					vmSize := string(*input.VM.Properties.HardwareProfile.VMSize)
					sku := fake.MakeSKU(vmSize)
					if sku.GetFamilyName() == "standardDSv3Family" {
						vcpus := int32(lo.Must(sku.VCPU()))
						current := usedCores.Load()
						if current+vcpus > int32(coreQuota) {
							return &azcore.ResponseError{
								ErrorCode: "OperationNotAllowed",
								RawResponse: &http.Response{
									Body: fake.CreateSDKErrorBody("OperationNotAllowed",
										fmt.Sprintf("Operation could not be completed as it results in exceeding approved standardDSv3Family Cores quota. "+
											"Additional details - Deployment Model: Resource Manager, Location: southcentralus, "+
											"Current Limit: %d, Current Usage: %d, Additional Required: %d, (Minimum) New Limit Required: %d.",
											coreQuota, current, vcpus, current+vcpus)),
								},
							}
						}
						usedCores.Add(vcpus)
					}
					return nil
				},
			)

			// Persist pods once
			for _, pod := range pods {
				ExpectApplied(ctx, env.Client, pod)
			}

			// First scheduling pass -- 2x D64's predicted but fail to allocate
			results, err := coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results.NewNodeClaims).To(HaveLen(2)) // 100 pods should fit in 2 64 core nodes, which is what Karpenter prefers
			nodeClaims := ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			// First actual NodeClaim attempt via CloudProvider fails (capacity issues)
			_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaims[0])
			Expect(err).To(MatchError(ContainSubstring("subscription level on-demand vCPU quota for Standard_D64s_v3 has been reached")))
			// Try again -- this time, we hit insufficient capacity error for both since D64 doesn't fit anymore
			for _, nodeClaim := range nodeClaims {
				_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaim)
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.InsufficientCapacityError{}))

				// Delete these claims (simulates what core does)
				ExpectDeleted(ctx, env.Client, nodeClaim)
			}

			// Second scheduling pass, 4-core nodes predicted now
			results, err = coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results.NewNodeClaims).To(HaveLen(25))
			nodeClaims = ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			// First 12 should succeed, as that's all under the quota of 50
			for i, nc := range nodeClaims[:12] {
				scheduledClaim := results.NewNodeClaims[i]
				_, node := ExpectNodeClaimDeployedAndStateUpdated(ctx, env.Client, cluster, cloudProvider, nc)
				Expect(node).ToNot(BeNil())

				// Have to bind pods to these nodes as well
				BindPodsToNode(ctx, env.Client, cluster, scheduledClaim, node)
			}
			// 13th should fail, we're past the quota now
			_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaims[12])
			Expect(err).To(MatchError(ContainSubstring("subscription level on-demand vCPU quota for Standard_D4s_v3 has been reached")))
			// try again for the rest of them, this time we get out of capacity error because there are no more 4-core VMs we can use
			for _, nodeClaim := range nodeClaims[12:] {
				_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaim)
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.InsufficientCapacityError{}))

				// Delete these claims (simulates what core does)
				ExpectDeleted(ctx, env.Client, nodeClaim)
			}

			// Last scheduling pass
			results, err = coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			// 24 pods have been scheduled so far on 12 nodes, remaining nodes are 1 pod per node so we expect 26 node claims
			Expect(results.NewNodeClaims).To(HaveLen(26))
			nodeClaims = ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			// First of the 2-core nodeclaims should fit into Ds_v3 family
			_, node := ExpectNodeClaimDeployedAndStateUpdated(ctx, env.Client, cluster, cloudProvider, nodeClaims[0])
			Expect(node).ToNot(BeNil())
			BindPodsToNode(ctx, env.Client, cluster, results.NewNodeClaims[0], node)
			// Next claim hits capacity issues -- we're at our 50 quota now
			_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaims[1])
			Expect(err).To(MatchError(ContainSubstring("subscription level on-demand vCPU quota for Standard_D2s_v3 has been reached")))

			// Now all remaining claims seamlessly fall back to D_v5 family without another scheduling pass needed
			for i, nc := range nodeClaims[1:] {
				scheduledClaim := results.NewNodeClaims[i+1]
				_, node := ExpectNodeClaimDeployedAndStateUpdated(ctx, env.Client, cluster, cloudProvider, nc)
				Expect(node).ToNot(BeNil())

				// Have to bind pods to these nodes as well
				BindPodsToNode(ctx, env.Client, cluster, scheduledClaim, node)
			}

			// Expect all pods to be scheduled
			for _, pod := range pods {
				pod = ExpectExists(ctx, env.Client, pod)
				Expect(pod.Spec.NodeName).ToNot(BeEmpty(), "pod %s should be scheduled", pod.Name)
			}

			// Expect all nodeclaims to be successfully provisioned (have a providerID)
			nodeClaimList := &karpv1.NodeClaimList{}
			Expect(env.Client.List(ctx, nodeClaimList)).To(Succeed())
			for _, nc := range nodeClaimList.Items {
				Expect(nc.Status.ProviderID).ToNot(BeEmpty(),
					"nodeclaim %s should have a providerID (launched successfully)", nc.Name)
			}

			// Verify that even the smallest Ds_v3 SKU is blocked (threshold ≤ 2 vCPU)
			for _, zone := range azureEnv.Zones() {
				ExpectUnavailable(azureEnv, fake.MakeSKU("Standard_D2s_v3"), zone, karpv1.CapacityTypeOnDemand)
			}

			// Verify that we have 12 D4s_v3, 1 D2s_v3, and 13 D2_v5 nodes, as expected
			nodes := &v1.NodeList{}
			Expect(env.Client.List(ctx, nodes)).To(Succeed())
			sizes := lo.Map(nodes.Items, func(node v1.Node, _ int) string {
				size, ok := node.Labels[v1.LabelInstanceTypeStable]
				Expect(ok).To(BeTrue(), "node %s should have instance type label", node.Name)
				return size
			})
			Expect(lo.CountValues(sizes)).To(Equal(map[string]int{
				"Standard_D4s_v3": 12,
				"Standard_D2s_v3": 1,
				"Standard_D2_v5":  25,
			}))
		})

		It("should fall back to a lower-weight NodePool when the primary NodePool's only SKU family is exhausted", func() {
			// NodePool A (weight 100): Ds_v3 series (standardDSv3Family) — preferred but limited quota
			nodePoolA := coretest.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Weight: lo.ToPtr(int32(100)),
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
			coretest.ReplaceRequirements(nodePoolA,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelSKUSeries,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"Ds_v3"},
				},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand},
				},
			)

			// NodePool B (weight 10): D_v5 series (standardDv5Family) — different family, lower priority
			nodePoolB := coretest.NodePool(karpv1.NodePool{
				Spec: karpv1.NodePoolSpec{
					Weight: lo.ToPtr(int32(10)),
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
			coretest.ReplaceRequirements(nodePoolB,
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      v1beta1.LabelSKUSeries,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{"D_v5"},
				},
				karpv1.NodeSelectorRequirementWithMinValues{
					Key:      karpv1.CapacityTypeLabelKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   []string{karpv1.CapacityTypeOnDemand},
				},
			)

			ExpectApplied(ctx, env.Client, nodePoolA, nodePoolB, nodeClass)

			const totalPods = 50
			const coreQuota = 50 // standardDSv3Family core quota limit
			pods := make([]*v1.Pod, totalPods)
			for i := range pods {
				pods[i] = coretest.UnschedulablePod(coretest.PodOptions{
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource.MustParse("1500m"),
						},
					},
				})
			}

			// Simulate quota exhaustion by tracking total cores consumed by the
			// standardDSv3Family. As progressively smaller SKUs fail, the blocked-CPU
			// threshold drops until all Ds_v3 sizes are unavailable. Since Ds_v3 is
			// the ONLY series for NodePool A, all remaining NodeClaims hit ICE and
			// the scheduler falls back to NodePool B.
			var usedCores atomic.Int32
			azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.SetCustomTransformer(
				func(input *fake.VirtualMachineCreateOrUpdateInput) error {
					vmSize := string(*input.VM.Properties.HardwareProfile.VMSize)
					sku := fake.MakeSKU(vmSize)
					if sku.GetFamilyName() == "standardDSv3Family" {
						vcpus := int32(lo.Must(sku.VCPU()))
						current := usedCores.Load()
						if current+vcpus > int32(coreQuota) {
							return &azcore.ResponseError{
								ErrorCode: "OperationNotAllowed",
								RawResponse: &http.Response{
									Body: fake.CreateSDKErrorBody("OperationNotAllowed",
										fmt.Sprintf("Operation could not be completed as it results in exceeding approved standardDSv3Family Cores quota. "+
											"Additional details - Deployment Model: Resource Manager, Location: southcentralus, "+
											"Current Limit: %d, Current Usage: %d, Additional Required: %d, (Minimum) New Limit Required: %d.",
											coreQuota, current, vcpus, current+vcpus)),
								},
							}
						}
						usedCores.Add(vcpus)
					}
					return nil
				},
			)

			// Persist pods once
			for _, pod := range pods {
				ExpectApplied(ctx, env.Client, pod)
			}

			// First scheduling pass -- NodePool A (weight 100) preferred, 2x D64's predicted but fail to allocate
			results, err := coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results.NewNodeClaims).To(HaveLen(2)) // 50 pods should fit in 2 64-core nodes, which is what Karpenter prefers
			nodeClaims := ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			// First actual NodeClaim attempt via CloudProvider fails (capacity issues)
			_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaims[0])
			Expect(err).To(MatchError(ContainSubstring("subscription level on-demand vCPU quota for Standard_D64s_v3 has been reached")))
			// Try again -- this time, we hit insufficient capacity error for both since D64 doesn't fit anymore
			for _, nodeClaim := range nodeClaims {
				_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaim)
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.InsufficientCapacityError{}))

				// Delete these claims (simulates what core does)
				ExpectDeleted(ctx, env.Client, nodeClaim)
			}

			// Second scheduling pass -- NodePool A with D4s_v3 nodes now
			results, err = coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(results.NewNodeClaims).To(HaveLen(25))
			nodeClaims = ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			// First 12 should succeed, as that's all under the quota of 50
			for i, nc := range nodeClaims[:12] {
				scheduledClaim := results.NewNodeClaims[i]
				_, node := ExpectNodeClaimDeployedAndStateUpdated(ctx, env.Client, cluster, cloudProvider, nc)
				Expect(node).ToNot(BeNil())

				// Have to bind pods to these nodes as well
				BindPodsToNode(ctx, env.Client, cluster, scheduledClaim, node)
			}
			// 13th should fail, we're past the quota now
			_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaims[12])
			Expect(err).To(MatchError(ContainSubstring("subscription level on-demand vCPU quota for Standard_D4s_v3 has been reached")))
			// try again for the rest of them, this time we get out of capacity error because there are no more 4-core VMs we can use
			for _, nodeClaim := range nodeClaims[12:] {
				_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaim)
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.InsufficientCapacityError{}))

				// Delete these claims (simulates what core does)
				ExpectDeleted(ctx, env.Client, nodeClaim)
			}

			// Third scheduling pass -- NodePool A with D2s_v3 now
			results, err = coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			// 24 pods have been scheduled so far on 12 nodes, remaining nodes are 1 pod per node so we expect
			// 26 node claims
			Expect(results.NewNodeClaims).To(HaveLen(26))
			nodeClaims = ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			// First of the 2-core nodeclaims should fit into Ds_v3 family
			_, node := ExpectNodeClaimDeployedAndStateUpdated(ctx, env.Client, cluster, cloudProvider, nodeClaims[0])
			Expect(node).ToNot(BeNil())
			BindPodsToNode(ctx, env.Client, cluster, results.NewNodeClaims[0], node)
			// Next claim hits capacity issues -- we're at our 50 quota now
			_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaims[1])
			Expect(err).To(MatchError(ContainSubstring("subscription level on-demand vCPU quota for Standard_D2s_v3 has been reached")))

			// Unlike the single-NodePool test, NodePool A only has Ds_v3 -- no in-pool fallback to D_v5.
			// All remaining claims from NodePool A get ICE.
			for _, nodeClaim := range nodeClaims[1:] {
				_, err = ExpectNodeClaimDeployedNoNode(ctx, env.Client, cloudProvider, nodeClaim)
				Expect(err).To(BeAssignableToTypeOf(&corecloudprovider.InsufficientCapacityError{}))

				// Delete these claims (simulates what core does)
				ExpectDeleted(ctx, env.Client, nodeClaim)
			}

			// Fourth scheduling pass -- NodePool A is fully exhausted, scheduler falls back to NodePool B (D_v5)
			results, err = coreProvisioner.Schedule(ctx)
			Expect(err).ToNot(HaveOccurred())
			// 25 pods remaining (50 - 24 from pass 2 - 1 from pass 3)
			Expect(results.NewNodeClaims).ToNot(BeEmpty())
			nodeClaims = ExpectScheduledNodeClaimsCreated(ctx, env.Client, coreProvisioner, results.NewNodeClaims...)
			for i, nc := range nodeClaims {
				scheduledClaim := results.NewNodeClaims[i]
				_, node := ExpectNodeClaimDeployedAndStateUpdated(ctx, env.Client, cluster, cloudProvider, nc)
				Expect(node).ToNot(BeNil())

				BindPodsToNode(ctx, env.Client, cluster, scheduledClaim, node)
			}

			// Expect all pods to be scheduled
			for _, pod := range pods {
				pod = ExpectExists(ctx, env.Client, pod)
				Expect(pod.Spec.NodeName).ToNot(BeEmpty(), "pod %s should be scheduled", pod.Name)
			}

			// Expect all nodeclaims to be successfully provisioned (have a providerID)
			nodeClaimList := &karpv1.NodeClaimList{}
			Expect(env.Client.List(ctx, nodeClaimList)).To(Succeed())
			for _, nc := range nodeClaimList.Items {
				Expect(nc.Status.ProviderID).ToNot(BeEmpty(),
					"nodeclaim %s should have a providerID (launched successfully)", nc.Name)
			}

			// Verify that even the smallest Ds_v3 SKU is blocked
			for _, zone := range azureEnv.Zones() {
				ExpectUnavailable(azureEnv, fake.MakeSKU("Standard_D2s_v3"), zone, karpv1.CapacityTypeOnDemand)
			}

			// Verify that we have 12 D4s_v3, 1 D2s_v3, and 25 D2_v5 nodes, as expected
			nodes := &v1.NodeList{}
			Expect(env.Client.List(ctx, nodes)).To(Succeed())
			sizes := lo.Map(nodes.Items, func(node v1.Node, _ int) string {
				size, ok := node.Labels[v1.LabelInstanceTypeStable]
				Expect(ok).To(BeTrue(), "node %s should have instance type label", node.Name)
				return size
			})
			Expect(lo.CountValues(sizes)).To(Equal(map[string]int{
				"Standard_D4s_v3": 12,
				"Standard_D2s_v3": 1,
				"Standard_D2_v5":  25,
			}))
		})
	})
})
