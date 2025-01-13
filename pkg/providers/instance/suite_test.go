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
	"strings"
	"testing"

	"github.com/awslabs/operatorpkg/object"
	opstatus "github.com/awslabs/operatorpkg/status"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clock "k8s.io/utils/clock/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"

	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	. "knative.dev/pkg/logging/testing"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
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
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	cloudProviderNonZonal = cloudprovider.New(azureEnvNonZonal.InstanceTypesProvider, azureEnvNonZonal.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnvNonZonal.ImageProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client)
	coreProvisioner = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)
	RunSpecs(t, "Provider/Azure")
}

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("InstanceProvider", func() {

	var nodeClass *v1alpha2.AKSNodeClass
	var nodePool *karpv1.NodePool
	var nodeClaim *karpv1.NodeClaim

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodeClass.StatusConditions().SetTrue(opstatus.ConditionReady)
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
					Name: nodeClass.Name,
				},
			},
		})
		azureEnv.Reset()
		azureEnvNonZonal.Reset()
		cluster.Reset()
	})

	var _ = AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	var ZonalAndNonZonalRegions = []TableEntry{
		Entry("zonal", azureEnv, cloudProvider),
		Entry("non-zonal", azureEnvNonZonal, cloudProviderNonZonal),
	}

	DescribeTable("should return an ICE error when all attempted instance types return an ICE error",
		func(azEnv *test.Environment, cp *cloudprovider.CloudProvider) {
			ExpectApplied(ctx, env.Client, nodeClaim, nodePool, nodeClass)
			for _, zone := range azEnv.Zones() {
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeSpot)
				azEnv.UnavailableOfferingsCache.MarkUnavailable(ctx, "SubscriptionQuotaReached", "Standard_D2_v2", zone, karpv1.CapacityTypeOnDemand)
			}
			instanceTypes, err := cp.GetInstanceTypes(ctx, nodePool)
			Expect(err).ToNot(HaveOccurred())

			// Filter down to a single instance type
			instanceTypes = lo.Filter(instanceTypes, func(i *corecloudprovider.InstanceType, _ int) bool { return i.Name == "Standard_D2_v2" })

			// Since all the offerings are unavailable, this should return back an ICE error
			instance, err := azEnv.InstanceProvider.Create(ctx, nodeClass, nodeClaim, instanceTypes)
			Expect(corecloudprovider.IsInsufficientCapacityError(err)).To(BeTrue())
			Expect(instance).To(BeNil())
		},
		ZonalAndNonZonalRegions,
	)
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
		It("should include 250 secondary ips by default", func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
			ExpectScheduled(ctx, env.Client, pod)

			Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
			Expect(nic).ToNot(BeNil())
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)

			// AzureCNI V1 has a DefaultMaxPods of 250 so we should set 250 ip configurations
			Expect(len(nic.Properties.IPConfigurations)).To(Equal(250))
		})
	})
	It("should create VM and NIC with valid ARM tags", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)

		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)

		Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
		vm, err := azureEnv.InstanceProvider.Get(ctx, vmName)
		Expect(err).To(BeNil())
		tags := vm.Tags
		Expect(lo.FromPtr(tags[instance.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.PickBy(tags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))

		Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
		nic := azureEnv.NetworkInterfacesAPI.NetworkInterfacesCreateOrUpdateBehavior.CalledWithInput.Pop().Interface
		Expect(nic).ToNot(BeNil())
		nicTags := nic.Tags
		Expect(lo.FromPtr(nicTags[instance.NodePoolTagKey])).To(Equal(nodePool.Name))
		Expect(lo.PickBy(nicTags, func(key string, value *string) bool {
			return strings.Contains(key, "/") // ARM tags can't contain '/'
		})).To(HaveLen(0))
	})
	It("should list nic from karpenter provisioning request", func() {
		ExpectApplied(ctx, env.Client, nodePool, nodeClass)
		pod := coretest.UnschedulablePod(coretest.PodOptions{})
		ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, coreProvisioner, pod)
		ExpectScheduled(ctx, env.Client, pod)
		interfaces, err := azureEnv.InstanceProvider.ListNics(ctx)
		Expect(err).To(BeNil())
		Expect(len(interfaces)).To(Equal(1))
	})
	It("should only list nics that belong to karpenter", func() {
		managedNic := test.Interface(
			test.InterfaceOptions{
				Tags: map[string]*string{
					instance.NodePoolTagKey: lo.ToPtr(nodePool.Name),
				},
			},
		)
		unmanagedNic := test.Interface()

		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
		azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(unmanagedNic.ID), *unmanagedNic)
		interfaces, err := azureEnv.InstanceProvider.ListNics(ctx)
		ExpectNoError(err)
		Expect(len(interfaces)).To(Equal(1))
		Expect(interfaces[0].Name).To(Equal(managedNic.Name))
	})
})
