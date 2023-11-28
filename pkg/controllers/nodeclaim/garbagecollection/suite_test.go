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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/karpenter/pkg/apis"
	"github.com/Azure/karpenter/pkg/apis/settings"
	"github.com/Azure/karpenter/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter/pkg/cloudprovider"
	"github.com/Azure/karpenter/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/Azure/karpenter/pkg/providers/instance"
	"github.com/Azure/karpenter/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"k8s.io/client-go/tools/record"
	clock "k8s.io/utils/clock/testing"
	. "knative.dev/pkg/logging/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter/pkg/test"
	corecloudprovider "github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/controllers/state"
	"github.com/aws/karpenter-core/pkg/events"
	"github.com/aws/karpenter-core/pkg/operator/controller"
	coreoptions "github.com/aws/karpenter-core/pkg/operator/options"
	"github.com/aws/karpenter-core/pkg/operator/scheme"
	coretest "github.com/aws/karpenter-core/pkg/test"
	. "github.com/aws/karpenter-core/pkg/test/expectations"

	corev1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"
)

var ctx context.Context
var stop context.CancelFunc
var env *coretest.Environment
var azureEnv *test.Environment
var fakeClock *clock.FakeClock
var nodePool *corev1beta1.NodePool
var nodeClass *v1alpha2.AKSNodeClass
var cluster *state.Cluster
var cloudProvider *cloudprovider.CloudProvider
var garbageCollectionController controller.Controller

func TestAPIs(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "NodeClaim")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	// TODO v1beta1 we dont have options yet
	// ctx = options.ToContext(ctx, test.Options())
	ctx = settings.ToContext(ctx, test.Settings())

	env = coretest.NewEnvironment(scheme.Scheme, coretest.WithCRDs(apis.CRDs...))
	ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	cloudProvider = cloudprovider.New(azureEnv.InstanceTypesProvider, azureEnv.InstanceProvider, events.NewRecorder(&record.FakeRecorder{}), env.Client, azureEnv.ImageProvider)
	garbageCollectionController = garbagecollection.NewController(env.Client, cloudProvider)
	fakeClock = &clock.FakeClock{}
	cluster = state.NewCluster(fakeClock, env.Client, cloudProvider)
})

var _ = AfterSuite(func() {
	stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = BeforeEach(func() {
	nodeClass = test.AKSNodeClass()
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
	azureEnv.Reset()
})

var _ = AfterEach(func() {
	ExpectCleanedUp(ctx, env.Client)
})

var _ = Describe("NodeClaimGarbageCollection", func() {
	var vm armcompute.VirtualMachine
	var providerID string

	BeforeEach(func() {
		id := utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, "vm-a")
		vm = armcompute.VirtualMachine{
			ID:   lo.ToPtr(id),
			Name: lo.ToPtr("vm-a"),
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
		azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), vm)

		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})
		_, err := cloudProvider.Get(ctx, providerID)
		Expect(err).To(HaveOccurred())
		Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())
	})
	It("should not delete an instance if it was not launched by a NodeClaim", func() {
		// Remove the "karpenter.sh/managed-by" tag (this isn't launched by a NodeClaim)
		vm.Properties = &armcompute.VirtualMachineProperties{
			TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
		}
		vm.Tags = lo.OmitBy(vm.Tags, func(key string, value *string) bool {
			return key == instance.NodePoolTagKey
		})
		azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), vm)

		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})
		_, err := cloudProvider.Get(ctx, providerID)
		Expect(err).NotTo(HaveOccurred())
	})
	It("should delete an instance along with the node if there is no NodeClaim owner (to quicken scheduling)", func() {
		// Launch happened 10m ago
		vm.Properties = &armcompute.VirtualMachineProperties{
			TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
		}
		azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), vm)

		node := coretest.Node(coretest.NodeOptions{
			ProviderID: providerID,
		})
		ExpectApplied(ctx, env.Client, node)

		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})
		_, err := cloudProvider.Get(ctx, providerID)
		Expect(err).To(HaveOccurred())
		Expect(corecloudprovider.IsNodeClaimNotFoundError(err)).To(BeTrue())

		ExpectNotFound(ctx, env.Client, node)
	})
	It("should delete many instances if they all don't have NodeClaim owners", func() {
		// Generate 100 instances that have different vmIDs
		var ids []string
		var vmName string
		var vmID string
		for i := 0; i < 100; i++ {
			vmName = fmt.Sprintf("vm-%d", i)
			vmID = utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)
			azureEnv.VirtualMachinesAPI.Instances.Store(
				vmID,
				armcompute.VirtualMachine{
					ID:   lo.ToPtr(utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
					Name: lo.ToPtr(vmName),
					Properties: &armcompute.VirtualMachineProperties{
						TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
					},
					Tags: map[string]*string{
						instance.NodePoolTagKey: lo.ToPtr("default"),
					},
				})
			ids = append(ids, vmID)
		}
		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})

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
		var nodeClaims []*corev1beta1.NodeClaim
		var vmName string
		var vmID string
		for i := 0; i < 100; i++ {
			vmName = fmt.Sprintf("vm-%d", i)
			vmID = utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)
			azureEnv.VirtualMachinesAPI.Instances.Store(
				vmID,
				armcompute.VirtualMachine{
					ID:   lo.ToPtr(utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
					Name: lo.ToPtr(vmName),
					Properties: &armcompute.VirtualMachineProperties{
						TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
					},
					Tags: map[string]*string{
						instance.NodePoolTagKey: lo.ToPtr("default"),
					},
				},
			)
			nodeClaim := coretest.NodeClaim(corev1beta1.NodeClaim{
				Status: corev1beta1.NodeClaimStatus{
					ProviderID: utils.ResourceIDToProviderID(ctx, vmID),
				},
			})
			ExpectApplied(ctx, env.Client, nodeClaim)
			nodeClaims = append(nodeClaims, nodeClaim)
			ids = append(ids, vmID)
		}
		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})

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
		azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), vm)

		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})
		_, err := cloudProvider.Get(ctx, providerID)
		Expect(err).NotTo(HaveOccurred())
	})
	It("should not delete the instance or node if it already has a nodeClaim that matches it", func() {
		// Launch time was 10m ago
		vm.Properties = &armcompute.VirtualMachineProperties{
			TimeCreated: lo.ToPtr(time.Now().Add(-time.Minute * 10)),
		}
		azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), vm)

		nodeClaim := coretest.NodeClaim(corev1beta1.NodeClaim{
			Status: corev1beta1.NodeClaimStatus{
				ProviderID: providerID,
			},
		})
		node := coretest.Node(coretest.NodeOptions{
			ProviderID: providerID,
		})
		ExpectApplied(ctx, env.Client, nodeClaim, node)

		ExpectReconcileSucceeded(ctx, garbageCollectionController, client.ObjectKey{})
		_, err := cloudProvider.Get(ctx, providerID)
		Expect(err).ToNot(HaveOccurred())
		ExpectExists(ctx, env.Client, node)
	})
})
