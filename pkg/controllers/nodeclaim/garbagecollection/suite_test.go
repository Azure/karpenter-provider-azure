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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cloudprovider"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
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
var testOptions *options.Options
var env *coretest.Environment
var azureEnv *test.Environment
var fakeClock *clock.FakeClock
var nodePool *karpv1.NodePool
var nodeClass *v1beta1.AKSNodeClass
var cluster *state.Cluster
var cloudProvider *cloudprovider.CloudProvider
var InstanceGCController *garbagecollection.Instance
var inPlaceUpdateController *inplaceupdate.Controller
var networkInterfaceGCController *garbagecollection.NetworkInterface
var prov *provisioning.Provisioner

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "GarbageCollection")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	testOptions = test.Options()
	ctx = options.ToContext(ctx, testOptions)
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	//	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider, azureEnv.InstanceTypeStore)
	InstanceGCController = garbagecollection.NewInstance(env.Client, cloudProvider)
	inPlaceUpdateController = inplaceupdate.NewController(env.Client, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider)
	networkInterfaceGCController = garbagecollection.NewNetworkInterface(env.Client, azureEnv.VMInstanceProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
	prov = provisioning.NewProvisioner(env.Client, events.NewRecorder(&record.FakeRecorder{}), cloudProvider, cluster, fakeClock)

})

var _ = AfterSuite(func() {
	//	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
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

	cluster.Reset()
	azureEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

// TODO: move before/after each into the tests (see AWS)
// review tests themselves (very different from AWS?)
// (e.g. AWS has not a single ExpectPRovisioned? why?)
var _ = Describe("Instance Garbage Collection", func() {
	var vm *armcompute.VirtualMachine
	var providerID string
	var err error

	// Attention: tests under "VM instances" are not applicable to AKS machine instances, created with ProvisionModeAKSMachineAPI.
	// Due to different assumptions, not all tests can be shared. Add tests for AKS machine instances in a different Context/file.
	// If VM instances are no longer supported, their code/tests will be replaced with AKS Machine instances.
	var _ = Context("VM instances", func() {
		var _ = Context("Pod pressure", func() {
			BeforeEach(func() {
				ExpectApplied(ctx, env.Client, nodePool, nodeClass)
				pod := coretest.UnschedulablePod(coretest.PodOptions{})
				ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, prov, azureEnv, pod)
				ExpectScheduled(ctx, env.Client, pod)
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len()).To(Equal(1))
				vmName := azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
				vm, err = azureEnv.VMInstanceProvider.Get(ctx, vmName)
				Expect(err).To(BeNil())
				providerID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
			})

			It("should not delete an instance if it was not launched by a NodeClaim", func() {
				// Remove the "karpenter.sh/managed-by" tag (this isn't launched by a NodeClaim)
				vm.Properties = &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
				}
				vm.Tags = lo.OmitBy(vm.Tags, func(key string, value *string) bool {
					return key == launchtemplate.NodePoolTagKey
				})
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
			})
			It("should delete many instances if they all don't have NodeClaim owners", func() {
				// Generate 100 instances that have different vmIDs
				var ids []string
				var vmName string
				for i := 0; i < 100; i++ {
					pod := coretest.UnschedulablePod()
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, prov, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					if azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len() == 1 {
						vmName = azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
						vm, err = azureEnv.VMInstanceProvider.Get(ctx, vmName)
						Expect(err).To(BeNil())
						providerID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
						newVM := test.VirtualMachine(test.VirtualMachineOptions{
							Name:         vmName,
							NodepoolName: "default",
							Properties: &armcompute.VirtualMachineProperties{
								TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
							},
						})
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(newVM.ID), newVM)
						ids = append(ids, *vm.ID)
					}
				}
				ExpectSingletonReconciled(ctx, InstanceGCController)

				wg := sync.WaitGroup{}
				for _, id := range ids {
					wg.Add(1)
					go func(id string) {
						defer GinkgoRecover()
						defer wg.Done()

						_, err := cloudProvider.Get(ctx, utils.VMResourceIDToProviderID(ctx, id))
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
					ExpectProvisionedAndDrained(ctx, env.Client, cluster, cloudProvider, prov, azureEnv, pod)
					ExpectScheduled(ctx, env.Client, pod)
					if azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Len() == 1 {
						vmName = azureEnv.VirtualMachinesAPI.VirtualMachineCreateOrUpdateBehavior.CalledWithInput.Pop().VMName
						vm, err = azureEnv.VMInstanceProvider.Get(ctx, vmName)
						Expect(err).To(BeNil())
						providerID = utils.VMResourceIDToProviderID(ctx, *vm.ID)
						newVM := test.VirtualMachine(test.VirtualMachineOptions{
							Name:         vmName,
							NodepoolName: "default",
							Properties: &armcompute.VirtualMachineProperties{
								TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
							},
						})
						azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(newVM.ID), newVM)
						nodeClaim := coretest.NodeClaim(karpv1.NodeClaim{
							Status: karpv1.NodeClaimStatus{
								ProviderID: utils.VMResourceIDToProviderID(ctx, *vm.ID),
							},
						})
						ids = append(ids, *vm.ID)
						ExpectApplied(ctx, env.Client, nodeClaim)
						nodeClaims = append(nodeClaims, nodeClaim)
					}
				}
				ExpectSingletonReconciled(ctx, InstanceGCController)

				wg := sync.WaitGroup{}
				for _, id := range ids {
					wg.Add(1)
					go func(id string) {
						defer GinkgoRecover()
						defer wg.Done()

						_, err := cloudProvider.Get(ctx, utils.VMResourceIDToProviderID(ctx, id))
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
				vm.Properties.TimeCreated = lo.ToPtr(time.Now())
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(err).NotTo(HaveOccurred())
			})
			It("should not delete the instance or node if it already has a nodeClaim that matches it", func() {
				// Launch time was 10m ago
				vm.Properties.TimeCreated = lo.ToPtr(time.Now().Add(-time.Minute * 10))
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

				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err := cloudProvider.Get(ctx, providerID)
				Expect(err).ToNot(HaveOccurred())
				ExpectExists(ctx, env.Client, node)
			})
		})

		var _ = Context("Basic", func() {
			BeforeEach(func() {
				vm = test.VirtualMachine(test.VirtualMachineOptions{Name: "vm-a", NodepoolName: "default"})
				providerID = utils.VMResourceIDToProviderID(ctx, lo.FromPtr(vm.ID))
			})
			It("should delete an instance if there is no NodeClaim owner", func() {
				// Launch happened 10m ago
				vm.Properties = &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
				}
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

				ExpectSingletonReconciled(ctx, InstanceGCController)
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

				ExpectSingletonReconciled(ctx, InstanceGCController)
				_, err = cloudProvider.Get(ctx, providerID)
				Expect(err).To(HaveOccurred())
				Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

				ExpectNotFound(ctx, env.Client, node)
			})
		})
	})
})

var _ = Describe("NetworkInterface Garbage Collection", func() {

	// Attention: tests under "VM instances" are not applicable to AKS machine instances, created with ProvisionModeAKSMachineAPI.
	// Due to different assumptions, not all tests can be shared. Add tests for AKS machine instances in a different Context/file.
	// If VM instances are no longer supported, their code/tests will be replaced with AKS Machine instances.
	var _ = Context("VM instances", func() {
		It("should not delete a network interface if a nodeclaim exists for it", func() {
			// Create and apply a NodeClaim that references this NIC
			nodeClaim := coretest.NodeClaim()
			ExpectApplied(ctx, env.Client, nodeClaim)

			// Create a managed NIC
			nic := test.Interface(test.InterfaceOptions{
				Name:         instance.GenerateResourceName(nodeClaim.Name),
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)

			nicsBeforeGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsBeforeGC)).To(Equal(1))

			// Run garbage collection
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)

			// Verify NIC still exists after GC
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(1))
		})
		It("should delete a NIC if there is no associated VM", func() {
			nic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			nic2 := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic2.ID), *nic2)
			nicsBeforeGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsBeforeGC)).To(Equal(2))
			// add a nic to azure env, and call reconcile. It should show up in the list before reconcile
			// then it should not showup after
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(0))
		})
		It("should not delete a NIC if there is an associated VM", func() {
			managedNic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
			managedVM := test.VirtualMachine(test.VirtualMachineOptions{Name: lo.FromPtr(managedNic.Name), NodepoolName: nodePool.Name})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(managedVM.ID), *managedVM)
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			// We should still have a network interface here
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(1))

		})
		It("the vm gc controller should handle deletion of network interfaces if a nic is associated with a vm", func() {
			managedNic := test.Interface(test.InterfaceOptions{
				NodepoolName: nodePool.Name,
			})
			azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(managedNic.ID), *managedNic)
			managedVM := test.VirtualMachine(test.VirtualMachineOptions{
				Name:         lo.FromPtr(managedNic.Name),
				NodepoolName: nodePool.Name,
				Properties: &armcompute.VirtualMachineProperties{
					TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 16)), // Needs to be older than the nodeclaim registration ttl
				},
			})
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(managedVM.ID), *managedVM)
			ExpectSingletonReconciled(ctx, networkInterfaceGCController)
			// We should still have a network interface here
			nicsAfterGC, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterGC)).To(Equal(1))

			ExpectSingletonReconciled(ctx, InstanceGCController)
			nicsAfterVMReconciliation, err := azureEnv.VMInstanceProvider.ListNics(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nicsAfterVMReconciliation)).To(Equal(0))

		})
	})
})
