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
	"context"
	"sync"
	"testing"
	"time"

	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	"github.com/awslabs/operatorpkg/object"
	opstatus "github.com/awslabs/operatorpkg/status"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/test"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/controllers/provisioning"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/events"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var ctx context.Context
var env *coretest.Environment
var azureEnv *test.Environment
var fakeClock *clock.FakeClock
var nodePool *karpv1.NodePool
var nodeClass *v1alpha2.AKSNodeClass
var cluster *state.Cluster
var cloudProvider *cloudprovider.CloudProvider
var garbageCollectionController *garbagecollection.Controller
var prov *provisioning.Provisioner

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "GarbageCollection")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	ctx = options.ToContext(ctx, test.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	//	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	garbageCollectionController = garbagecollection.NewController(env.Client, cloudProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client)
	prov = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster)
})

var _ = AfterSuite(func() {
	//	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
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

	cluster.Reset()
	azureEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

// TODO: move before/after each into the tests (see AWS)
// review tests themselves (very different from AWS?)
// (e.g. AWS has not a single ExpectPRovisioned? why?)
var _ = Describe("GarbageCollection", func() {
	var vm *armcompute.VirtualMachine
	var providerID string
	var err error

	var _ = Context("Pod pressure", func() {
		BeforeEach(func() {
			ExpectApplied(ctx, env.Client, nodePool, nodeClass)
			pod := coretest.UnschedulablePod(coretest.PodOptions{})
			ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
			ExpectScheduled(ctx, env.Client, pod)
			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
			vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
			vm, err = azureEnv.InstanceProvider.Get(ctx, vmName)
			Expect(err).To(BeNil())
			providerID = utils.ResourceIDToProviderID(ctx, *vm.ID)
		})

		It("should not delete an instance if it was not launched by a NodeClaim", func() {
			// Remove the "karpenter.sh/managed-by" tag (this isn't launched by a NodeClaim)
			vm.Properties = &armcompute.VirtualMachineProperties{
				TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
			}
			vm.Tags = lo.OmitBy(vm.Tags, func(key string, value *string) bool {
				return key == instance.NodePoolTagKey
			})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ExpectSingletonReconciled(ctx, garbageCollectionController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should delete many instances if they all don't have NodeClaim owners", func() {
			// Generate 100 instances that have different vmIDs
			var ids []string
			var vmName string
			for i := 0; i < 100; i++ {
				pod := coretest.UnschedulablePod()
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
				ExpectScheduled(ctx, env.Client, pod)
				if azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len() == 1 {
					vmName = azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
					vm, err = azureEnv.InstanceProvider.Get(ctx, vmName)
					Expect(err).To(BeNil())
					providerID = utils.ResourceIDToProviderID(ctx, *vm.ID)
					azureEnv.VirtualMachinesAPI.Instances.Store(
						*vm.ID,
						armcompute.VirtualMachine{
							ID:       vm.ID,
							Name:     vm.Name,
							Location: lo.ToPtr(fake.Region),
							Properties: &armcompute.VirtualMachineProperties{
								TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
							},
							Tags: map[string]*string{
								instance.NodePoolTagKey: lo.ToPtr("default"),
							},
						})
					ids = append(ids, *vm.ID)
				}
			}
			ExpectSingletonReconciled(ctx, garbageCollectionController)

			wg := sync.WaitGroup{}
			for _, id := range ids {
				wg.Add(1)
				go func(id string) {
					defer GinkgoRecover()
					defer wg.Done()

					_, err := cloudProvider.Get(ctx, utils.ResourceIDToProviderID(ctx, id))
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
				ExpectProvisioned(ctx, env.Client, cluster, cloudProvider, prov, pod)
				ExpectScheduled(ctx, env.Client, pod)
				if azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len() == 1 {
					vmName = azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
					vm, err = azureEnv.InstanceProvider.Get(ctx, vmName)
					Expect(err).To(BeNil())
					providerID = utils.ResourceIDToProviderID(ctx, *vm.ID)
					azureEnv.VirtualMachinesAPI.Instances.Store(
						*vm.ID,
						armcompute.VirtualMachine{
							ID:       vm.ID,
							Name:     vm.Name,
							Location: lo.ToPtr(fake.Region),
							Properties: &armcompute.VirtualMachineProperties{
								TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
							},
							Tags: map[string]*string{
								instance.NodePoolTagKey: lo.ToPtr("default"),
							},
						})
					nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
						Status: karpv1.NodeClaimStatus{
							ProviderID: utils.ResourceIDToProviderID(ctx, *vm.ID),
						},
					})
					ids = append(ids, *vm.ID)
					ExpectApplied(ctx, env.Client, nodeClaim)
					nodeClaims = append(nodeClaims, nodeClaim)
				}
			}
			ExpectSingletonReconciled(ctx, garbageCollectionController)

			wg := sync.WaitGroup{}
			for _, id := range ids {
				wg.Add(1)
				go func(id string) {
					defer GinkgoRecover()
					defer wg.Done()

					_, err := cloudProvider.Get(ctx, utils.ResourceIDToProviderID(ctx, id))
					Expect(err).ToNot(HaveOccurred())
				}(id)
			}
			wg.Wait()

			for _, nodeClaim := range nodeClaims {
				ExpectExists(ctx, env.Client, nodeClaim)
			}
		})
		It("should not delete an instance if it is within the nodeClaim resolution window (5m)", func() {
			// Launch time just happened
			vm.Properties = &armcompute.VirtualMachineProperties{
				TimeCreated: lo.ToPtr(time.Now()),
			}
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ExpectSingletonReconciled(ctx, garbageCollectionController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should not delete the instance or node if it already has a nodeClaim that matches it", func() {
			// Launch time was 10m ago
			vm.Properties = &armcompute.VirtualMachineProperties{
				TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
			}
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
				Status: karpv1.NodeClaimStatus{
					ProviderID: providerID,
				},
			})
			node := coretest.Node(coretest.NodeOptions{
				ProviderID: providerID,
			})
			ExpectApplied(ctx, env.Client, nodeClaim, node)

			ExpectSingletonReconciled(ctx, garbageCollectionController)
			_, err := cloudProvider.Get(ctx, providerID)
			Expect(err).ToNot(HaveOccurred())
			ExpectExists(ctx, env.Client, node)
		})
	})

	var _ = Context("Basic", func() {
		BeforeEach(func() {
			id := utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, "vm-a")
			vm = &armcompute.VirtualMachine{
				ID:       lo.ToPtr(id),
				Name:     lo.ToPtr("vm-a"),
				Location: lo.ToPtr(fake.Region),
				Tags: map[string]*string{
					instance.NodePoolTagKey: lo.ToPtr("default"),
				},
			}
			providerID = utils.ResourceIDToProviderID(ctx, id)
		})
		It("should delete an instance if there is no NodeClaim owner", func() {
			// Launch happened 10m ago
			vm.Properties = &armcompute.VirtualMachineProperties{
				TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
			}
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ExpectSingletonReconciled(ctx, garbageCollectionController)
			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
		})
		It("should delete an instance along with the node if there is no NodeClaim owner (to quicken scheduling)", func() {
			// Launch happened 10m ago
			vm.Properties = &armcompute.VirtualMachineProperties{
				TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
			}
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
			node := coretest.Node(coretest.NodeOptions{
				ProviderID: providerID,
			})
			ExpectApplied(ctx, env.Client, node)

			ExpectSingletonReconciled(ctx, garbageCollectionController)
			_, err = cloudProvider.Get(ctx, providerID)
			Expect(err).To(HaveOccurred())
			Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

			ExpectNotFound(ctx, env.Client, node)
		})
	})
})
