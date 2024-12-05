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

package inplaceupdate_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	. "knative.dev/pkg/logging/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var ctx context.Context
var env *coretest.Environment
var azureEnv *test.Environment
var inPlaceUpdateController *inplaceupdate.Controller

func TestInPlaceUpdate(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers/InPlaceUpdate")
}

var _ = BeforeSuite(func() {
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	// ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	inPlaceUpdateController = inplaceupdate.NewController(env.Client, azureEnv.InstanceProvider)
})

var _ = AfterSuite(func() {
	//stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("Unit tests", func() {
	Context("HashFromVM", func() {
		It("should not depend on identity ordering", func() {
			vm1 := &armcompute.VirtualMachine{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3": {},
					},
				},
			}

			vm2 := &armcompute.VirtualMachine{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3": {},
					},
				},
			}

			vm3 := &armcompute.VirtualMachine{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
					},
				},
			}

			hash1, err := inplaceupdate.HashFromVM(vm1)
			Expect(err).ToNot(HaveOccurred())

			hash2, err := inplaceupdate.HashFromVM(vm2)
			Expect(err).ToNot(HaveOccurred())

			hash3, err := inplaceupdate.HashFromVM(vm3)
			Expect(err).ToNot(HaveOccurred())

			Expect(hash1).To(Equal(hash2))
			Expect(hash2).To(Equal(hash3))
		})
	})

	Context("HashFromNodeClaim", func() {
		It("should not depend on identity ordering", func() {
			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
			}

			hash1, err := inplaceupdate.HashFromNodeClaim(options, nil)
			Expect(err).ToNot(HaveOccurred())

			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
			}
			hash2, err := inplaceupdate.HashFromNodeClaim(options, nil)
			Expect(err).ToNot(HaveOccurred())

			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			hash3, err := inplaceupdate.HashFromNodeClaim(options, nil)
			Expect(err).ToNot(HaveOccurred())

			Expect(hash1).To(Equal(hash2))
			Expect(hash2).To(Equal(hash3))
		})
	})

	Context("calculateVMPatch", func() {
		It("should add missing identities when there are no existing identities", func() {
			currentVM := &armcompute.VirtualMachine{}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			update := inplaceupdate.CalculateVMPatch(options, currentVM)

			Expect(update).ToNot(BeNil())
			Expect(update.Identity).ToNot(BeNil())
			Expect(update.Identity.UserAssignedIdentities).To(HaveLen(1))
			Expect(update.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
		})

		It("should add missing identities when there are existing identities", func() {
			currentVM := &armcompute.VirtualMachine{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
					},
				},
			}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
			}
			update := inplaceupdate.CalculateVMPatch(options, currentVM)

			Expect(update).ToNot(BeNil())
			Expect(update.Identity).ToNot(BeNil())
			Expect(update.Identity.UserAssignedIdentities).To(HaveLen(1))
			Expect(update.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2"))
		})

		It("should add no identities when identities already exist", func() {
			currentVM := &armcompute.VirtualMachine{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2": {},
					},
				},
			}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			update := inplaceupdate.CalculateVMPatch(options, currentVM)

			Expect(update).To(BeNil())
		})

		It("should not remove identities", func() {
			currentVM := &armcompute.VirtualMachine{
				Identity: &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1":           {},
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myotheridentity": {},
					},
				},
			}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			update := inplaceupdate.CalculateVMPatch(options, currentVM)

			Expect(update).To(BeNil())
		})
	})
})

var _ = Describe("In Place Update Controller", func() {
	var vmName string
	var vm *armcompute.VirtualMachine
	var nodeClaim *karpv1.NodeClaim

	BeforeEach(func() {
		vmName = "vm-a"
		vm = &armcompute.VirtualMachine{
			ID:   lo.ToPtr(utils.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
			Name: lo.ToPtr(vmName),
		}

		nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
			Status: karpv1.NodeClaimStatus{
				ProviderID: utils.ResourceIDToProviderID(ctx, *vm.ID),
			},
		})

		ctx = options.ToContext(ctx, test.Options())

		azureEnv.Reset()
	})

	AfterEach(func() {
		ExpectCleanedUp(ctx, env.Client)
	})

	Context("Basic tests", func() {
		It("should not call Azure if the hash matches", func() {
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
			hash, err := inplaceupdate.HashFromNodeClaim(options.FromContext(ctx), nodeClaim)
			Expect(err).ToNot(HaveOccurred())

			// Force the goal hash into annotations here, which should prevent the reconciler from doing anything on Azure
			nodeClaim.Annotations = map[string]string{v1alpha2.AnnotationInPlaceUpdateHash: hash}

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls()).To(Equal(0))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1alpha2.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})
	})

	Context("Identity tests", func() {
		It("should add a hash annotation to NodeClaim if there are no identities", func() {
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			updatedVM, err := azureEnv.InstanceProvider.Get(ctx, vmName)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedVM).To(Equal(vm)) // No change expected

			// The nodeClaim should have the InPlaceUpdateHash annotation
			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1alpha2.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should add a hash annotation to NodeClaim and update VM if there are missing identities", func() {
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						NodeIdentities: []string{
							"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
						},
					}))

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			updatedVM, err := azureEnv.InstanceProvider.Get(ctx, vmName)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedVM).ToNot(Equal(vm))
			Expect(updatedVM.Identity).ToNot(BeNil())
			Expect(updatedVM.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1alpha2.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should not call Azure if the expected identities already exist on the VM", func() {
			vm.Identity = &armcompute.VirtualMachineIdentity{
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
				},
			}
			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls()).To(Equal(0))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1alpha2.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should not clear existing identities on VM", func() {
			vm.Identity = &armcompute.VirtualMachineIdentity{
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myotheridentity": {},
				},
			}

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						NodeIdentities: []string{
							"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
						},
					}))

			azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			updatedVM, err := azureEnv.InstanceProvider.Get(ctx, vmName)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedVM.Identity).ToNot(BeNil())
			Expect(updatedVM.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
			Expect(updatedVM.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myotheridentity"))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1alpha2.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1alpha2.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})
	})
})
