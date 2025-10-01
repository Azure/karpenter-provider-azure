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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var ctx context.Context
var env *coretest.Environment
var azureEnv *test.Environment
var inPlaceUpdateController *inplaceupdate.Controller
var opts *options.Options

func TestInPlaceUpdate(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers/InPlaceUpdate")
}

var _ = BeforeSuite(func() {
	ctx = options.ToContext(ctx, test.Options())
	ctx = coreoptions.ToContext(ctx, coretest.Options())
	env = coretest.NewEnvironment(coretest.WithCRDs(apis.CRDs...), coretest.WithCRDs(v1alpha1.CRDs...))
	// ctx, stop = context.WithCancel(ctx)
	azureEnv = test.NewEnvironment(ctx, env)
	inPlaceUpdateController = inplaceupdate.NewController(env.Client, azureEnv.VMInstanceProvider)
	opts = options.FromContext(ctx)
})

var _ = AfterSuite(func() {
	//stop()
	Expect(env.Stop()).To(Succeed(), "Failed to stop environment")
})

var _ = Describe("Unit tests", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var nodeClaim *karpv1.NodeClaim
	var vm *armcompute.VirtualMachine
	var currentVM *armcompute.VirtualMachine

	BeforeEach(func() {
		vmName := "vm-a"
		vm = &armcompute.VirtualMachine{
			ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
			Name: lo.ToPtr(vmName),
		}

		nodeClass = test.AKSNodeClass()
		nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
			Spec: karpv1.NodeClaimSpec{
				NodeClassRef: &karpv1.NodeClassReference{
					Group: object.GVK(nodeClass).Group,
					Kind:  object.GVK(nodeClass).Kind,
					Name:  nodeClass.Name,
				},
			},
			Status: karpv1.NodeClaimStatus{
				ProviderID: utils.VMResourceIDToProviderID(ctx, *vm.ID),
			},
		})

		currentVM = &armcompute.VirtualMachine{
			ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
			Name: lo.ToPtr(vmName),
			Tags: map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
			},
		}
	})

	Context("HashFromNodeClaim", func() {
		It("should not depend on identity ordering", func() {
			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
			}

			hash1, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nil)
			Expect(err).ToNot(HaveOccurred())

			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
			}
			hash2, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nil)
			Expect(err).ToNot(HaveOccurred())

			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			hash3, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nil)
			Expect(err).ToNot(HaveOccurred())

			Expect(hash1).To(Equal(hash2))
			Expect(hash2).To(Equal(hash3))
		})

		It("should include both AdditionalTags and AKSNodeClass.Spec.Tags in hash calculation", func() {
			options := test.Options()
			options.AdditionalTags = map[string]string{
				"global-tag": "global-value",
			}

			// Create NodeClass with tags
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag1": "nodeclass-value",
				"nodeclass-tag2": "nodeclass-value",
			}

			hash1, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			// Hash should be different without NodeClass tags
			hash2, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash1).ToNot(Equal(hash2))

			// Hash should be the same with the same NodeClass tags
			nodeClass2 := test.AKSNodeClass()
			nodeClass2.Spec.Tags = map[string]string{
				"nodeclass-tag1": "nodeclass-value",
				"nodeclass-tag2": "nodeclass-value",
			}
			hash3, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass2)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash1).To(Equal(hash3))
		})

		It("should prioritize NodeClass tags over AdditionalTags", func() {
			options := test.Options()
			options.AdditionalTags = map[string]string{
				"priority-tag": "additional-value",
			}

			nodeClass.Spec.Tags = map[string]string{
				"priority-tag": "nodeclass-value",
			}

			hash1, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			hash2, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nil)
			Expect(err).ToNot(HaveOccurred())

			// Should be different because NodeClass overrides AdditionalTags
			Expect(hash1).ToNot(Equal(hash2))
		})
	})

	Context("calculateVMPatch", func() {
		It("should add missing identities when there are no existing identities", func() {
			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).ToNot(BeNil())
			Expect(update.Identity).ToNot(BeNil())
			Expect(update.Identity.UserAssignedIdentities).To(HaveLen(1))
			Expect(update.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
		})

		It("should add missing identities when there are existing identities", func() {
			currentVM.Identity = &armcompute.VirtualMachineIdentity{
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
				},
			}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).ToNot(BeNil())
			Expect(update.Identity).ToNot(BeNil())
			Expect(update.Identity.UserAssignedIdentities).To(HaveLen(1))
			Expect(update.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2"))
		})

		It("should add no identities when identities already exist", func() {
			currentVM.Identity = &armcompute.VirtualMachineIdentity{
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2": {},
				},
			}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(BeNil())
		})

		It("should not remove identities", func() {
			currentVM.Identity = &armcompute.VirtualMachineIdentity{
				UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1":           {},
					"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myotheridentity": {},
				},
			}

			options := test.Options()
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(BeNil())
		})

		It("should add missing tags from AdditionalTags", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "my-tag",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
					"test-tag":                    lo.ToPtr("my-tag"),
				},
			}))
		})

		It("should add missing tags from NodeClass", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
			}

			options := test.Options()
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
					"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
				},
			}))
		})

		It("should add missing tags from NodeClass if conflicting with AdditionalTags", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				"test-tag":                    lo.ToPtr("my-tag"),
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "my-tag",
			}
			nodeClass.Spec.Tags = map[string]string{
				"test-tag": "nodeclass-value",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
					"test-tag":                    lo.ToPtr("nodeclass-value"),
				},
			}))
		})

		// NOTE: It is expected that this will remove manually added user tags as well
		It("should remove unneeded tags", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				"test-tag":                    lo.ToPtr("my-tag"),
			}

			options := test.Options()
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
				},
			}))
		})
	})
})

var _ = Describe("In Place Update Controller", func() {
	// Attention: tests under "VM instances" are not applicable to AKS machine instances, created with ProvisionModeAKSMachineAPI.
	// Due to different assumptions, not all tests can be shared. Add tests for AKS machine instances in a different Context/file.
	// If VM instances are no longer supported, their code/tests will be replaced with AKS Machine instances.
	Context("VM instances", func() {
		var vmName string
		var vm *armcompute.VirtualMachine
		var nic *armnetwork.Interface
		var billingExt *armcompute.VirtualMachineExtension
		var cseExt *armcompute.VirtualMachineExtension
		var nodeClaim *karpv1.NodeClaim
		var nodeClass *v1beta1.AKSNodeClass

		BeforeEach(func() {
			vmName = "vm-a"
			vm = &armcompute.VirtualMachine{
				ID:   lo.ToPtr(fake.MkVMID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				},
			}
			nic = &armnetwork.Interface{
				ID:   lo.ToPtr(fake.MakeNetworkInterfaceID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				},
			}
			billingExt = &armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "computeAksLinuxBilling")),
				Name: lo.ToPtr("computeAksLinuxBilling"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				},
			}
			cseExt = &armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "cse-agent-karpenter")),
				Name: lo.ToPtr("cse-agent-karpenter"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				},
			}

			// Create a test AKSNodeClass that the NodeClaim can reference
			nodeClass = test.AKSNodeClass()
			nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
				Spec: karpv1.NodeClaimSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
				},
				Status: karpv1.NodeClaimStatus{
					ProviderID: utils.VMResourceIDToProviderID(ctx, *vm.ID),
				},
			})
			// Claims are launched and registered by default
			nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
			nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)

			// Apply the nodeClass to the test environment
			ExpectApplied(ctx, env.Client, nodeClass)

			ctx = options.ToContext(ctx, test.Options())

			azureEnv.Reset()
		})

		AfterEach(func() {
			ExpectCleanedUp(ctx, env.Client)
		})

		Context("Basic tests", func() {
			It("should not call Azure if the hash matches", func() {
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				hash, err := inplaceupdate.HashFromNodeClaim(opts, nodeClaim, nodeClass)
				Expect(err).ToNot(HaveOccurred())

				// Force the goal hash into annotations here, which should prevent the reconciler from doing anything on Azure
				nodeClaim.Annotations = map[string]string{v1beta1.AnnotationInPlaceUpdateHash: hash}

				ExpectApplied(ctx, env.Client, nodeClaim)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls()).To(Equal(0))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			It("should skip reconciliation when NodeClaim has no ProviderID", func() {
				nodeClaim.Status.ProviderID = ""

				ExpectApplied(ctx, env.Client, nodeClaim)
				result, err := inPlaceUpdateController.Reconcile(ctx, nodeClaim)

				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{RequeueAfter: 60 * time.Second}))
				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls()).To(Equal(0))
			})

			It("should handle invalid ProviderID gracefully", func() {
				nodeClaim.Status.ProviderID = "invalid-provider-id"

				ExpectApplied(ctx, env.Client, nodeClaim)
				result, err := inPlaceUpdateController.Reconcile(ctx, nodeClaim)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
			})

			It("should handle VM not found gracefully", func() {
				// Don't store the VM in the fake API, so Get will fail

				ExpectApplied(ctx, env.Client, nodeClaim)
				result, err := inPlaceUpdateController.Reconcile(ctx, nodeClaim)

				Expect(err).To(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(err.Error()).To(ContainSubstring("nodeclaim not found"))
			})

			It("should add a hash annotation to NodeClaim if there are no identities or tags", func() {
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

				ExpectApplied(ctx, env.Client, nodeClaim)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				updatedVM, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
				Expect(err).ToNot(HaveOccurred())

				Expect(updatedVM).To(Equal(vm)) // No change expected

				// The nodeClaim should have the InPlaceUpdateHash annotation
				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})
		})

		Context("Identity tests", func() {
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

				updatedVM, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
				Expect(err).ToNot(HaveOccurred())

				Expect(updatedVM).ToNot(Equal(vm))
				Expect(updatedVM.Identity).ToNot(BeNil())
				Expect(updatedVM.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
				// Expect the tags to remain unchanged
				Expect(updatedVM.Tags).To(Equal(map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				}))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			It("should not call Azure if the expected identities already exist on the VM", func() {
				vm.Identity = &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1": {},
					},
				}
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

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls()).To(Equal(0))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			It("should not clear existing identities on VM", func() {
				vm.Identity = &armcompute.VirtualMachineIdentity{
					UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
						"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myotheridentity": {},
					},
				}
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

				updatedVM, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
				Expect(err).ToNot(HaveOccurred())

				Expect(updatedVM.Identity).ToNot(BeNil())
				Expect(updatedVM.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1"))
				Expect(updatedVM.Identity.UserAssignedIdentities).To(HaveKey("/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myotheridentity"))
				// Expect the tags to remain unchanged
				Expect(updatedVM.Tags).To(Equal(map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				}))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})
		})

		Context("Tags tests", func() {
			It("should add a hash annotation to NodeClaim and update VM, NIC, and Extensions if there are missing tags", func() {
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(billingExt.ID), *billingExt)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(cseExt.ID), *cseExt)

				ctx = options.ToContext(
					ctx,
					test.Options(
						test.OptionsFields{
							AdditionalTags: map[string]string{
								"test-tag": "my-tag",
							},
						}))
				nodeClass.Spec.Tags = map[string]string{
					"nodeclass-tag": "nodeclass-value",
				}

				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				// The VM should be updated
				updatedVM := ExpectInstanceResourcesHaveTags(
					ctx,
					vmName,
					azureEnv,
					map[string]*string{
						"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
						"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
						"test-tag":                    lo.ToPtr("my-tag"),
					})
				Expect(updatedVM).ToNot(Equal(vm))
				// Expect the identities to remain unchanged
				Expect(updatedVM.Identity).To(BeNil())

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			It("should not call Azure if the expected tags already exist on the VM", func() {
				vm.Tags["test-tag"] = lo.ToPtr("my-tag")
				vm.Tags["nodeclass-tag"] = lo.ToPtr("nodeclass-value")
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)

				ctx = options.ToContext(
					ctx,
					test.Options(
						test.OptionsFields{
							AdditionalTags: map[string]string{
								"test-tag": "my-tag",
							},
						}))
				nodeClass.Spec.Tags = map[string]string{
					"nodeclass-tag": "nodeclass-value",
				}

				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				Expect(azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls()).To(Equal(0))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			It("should clear existing tags on VM", func() {
				vm.Tags = map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
					"test-tag":                    lo.ToPtr("my-tag"),
					"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
				}
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(billingExt.ID), *billingExt)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(cseExt.ID), *cseExt)

				nodeClass.Spec.Tags = map[string]string{
					"nodeclass-tag": "nodeclass-value",
				}

				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				// The VM should be updated
				updatedVM := ExpectInstanceResourcesHaveTags(
					ctx,
					vmName,
					azureEnv,
					map[string]*string{
						"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
						"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
						// "test-tag" should be removed
					})
				// Expect the identities to remain unchanged
				Expect(updatedVM.Identity).To(BeNil())

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			It("should propagate update to tags from NodeClass with standard tags", func() {
				newTags := map[string]string{"nodeclass-tag": "nodeclass-value"}
				expectedTags := map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
					"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
				}

				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(billingExt.ID), *billingExt)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(cseExt.ID), *cseExt)

				ExpectApplied(ctx, env.Client, nodeClaim)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
				initialHash := nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]

				nodeClass.Spec.Tags = newTags
				ExpectApplied(ctx, env.Client, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				updatedVM := ExpectInstanceResourcesHaveTags(
					ctx,
					vmName,
					azureEnv,
					expectedTags)
				Expect(updatedVM).ToNot(Equal(vm))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(Equal(initialHash))
			})

			It("should propagate update to tags from NodeClass with tags containing '/' replaced with '_'", func() {
				newTags := map[string]string{"this/tag/has/slashes": "nodeclass-value"}
				expectedTags := map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
					"this_tag_has_slashes":        lo.ToPtr("nodeclass-value"),
				}

				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(billingExt.ID), *billingExt)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(cseExt.ID), *cseExt)

				ExpectApplied(ctx, env.Client, nodeClaim)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
				initialHash := nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]

				nodeClass.Spec.Tags = newTags
				ExpectApplied(ctx, env.Client, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				updatedVM := ExpectInstanceResourcesHaveTags(
					ctx,
					vmName,
					azureEnv,
					expectedTags)
				Expect(updatedVM).ToNot(Equal(vm))

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(Equal(initialHash))
			})

			It("should not apply tags if claim is not registered yet. Retry succeeds", func() {
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
				// Not registering the billing and CSE extensions to simulate situation where VM is still being provisioned
				// when tags update happens.
				nodeClaim.StatusConditions().SetUnknown(karpv1.ConditionTypeRegistered) // Claim not registered yet

				ExpectApplied(ctx, env.Client, nodeClaim)
				nodeClass.Spec.Tags = map[string]string{
					"nodeclass-tag": "nodeclass-value",
				}
				ExpectApplied(ctx, env.Client, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				// Expect no calls yet
				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).ToNot(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(azureEnv.NetworkInterfacesAPI.NetworkInterfacesUpdateTagsBehavior.CalledWithInput.Len()).To(Equal(0))

				// Now claim registers
				nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
				ExpectApplied(ctx, env.Client, nodeClaim)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(billingExt.ID), *billingExt)
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(cseExt.ID), *cseExt)

				// Retry should succeed
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)
				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())

				updatedVM := ExpectInstanceResourcesHaveTags(
					ctx,
					vmName,
					azureEnv,
					map[string]*string{
						"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
						"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
					})
				Expect(updatedVM).ToNot(Equal(vm))
			})
		})
	})
})
