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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	"sigs.k8s.io/karpenter/pkg/test/v1alpha1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/karpenter-provider-azure/pkg/apis"
	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
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
	azureEnv = test.NewEnvironment(ctx, env)
	inPlaceUpdateController = inplaceupdate.NewController(env.Client, azureEnv.VMInstanceProvider, azureEnv.AKSMachineProvider)
	opts = options.FromContext(ctx)
})

var _ = AfterSuite(func() {
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
				"compute.aks.billing":         lo.ToPtr("linux"),
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

			hash1, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
			}
			hash2, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid3",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid2",
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}
			hash3, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
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
			nodeClass.Spec.Tags = nil
			hash2, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
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

			nodeClass.Spec.Tags = nil
			hash2, err := inplaceupdate.HashFromNodeClaim(options, nodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			// Should be different because NodeClass overrides AdditionalTags
			Expect(hash1).ToNot(Equal(hash2))
		})

		It("should produce different hashes for AKS machines vs VMs", func() {
			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "test-value",
			}
			options.NodeIdentities = []string{
				"/subscriptions/1234/resourceGroups/mcrg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/myid1",
			}

			// Regular VM NodeClaim
			vmNodeClaim := coretest.NodeClaim()
			hash1, err := inplaceupdate.HashFromNodeClaim(options, vmNodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			// AKS Machine NodeClaim
			aksMachineNodeClaim := coretest.NodeClaim()
			aksMachineNodeClaim.Annotations = map[string]string{
				v1beta1.AnnotationAKSMachineResourceID: "/subscriptions/123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/agentPools/pool/machines/test-machine",
			}
			hash2, err := inplaceupdate.HashFromNodeClaim(options, aksMachineNodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			// Should be different because AKS machines don't include identities in hash
			Expect(hash1).ToNot(Equal(hash2))
		})
	})

	Context("calculateAKSMachinePatch", func() {
		It("should add missing tags to AKS machine when there are no existing tags", func() {
			aksMachine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{},
				},
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "my-tag",
			}
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}

			patchExists := inplaceupdate.CalculateAKSMachinePatch(options, nodeClaim, nodeClass, aksMachine)

			Expect(patchExists).To(BeTrue())
			// Verify original aksMachine was modified (expected behavior for AKS machines - PUT only, not PATCH)
			Expect(aksMachine.Properties.Tags).To(HaveKey("test-tag"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("nodeclass-tag"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
		})

		It("should not update AKS machine when tags already match", func() {
			createTimeString := instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp())
			aksMachine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{
						"karpenter.azure.com_cluster":                      lo.ToPtr(opts.ClusterName),
						"karpenter.azure.com_aksmachine_nodeclaim":         lo.ToPtr(nodeClaim.Name),
						"karpenter.azure.com_aksmachine_creationtimestamp": lo.ToPtr(createTimeString),
						"compute.aks.billing":                              lo.ToPtr("linux"),
						"test-tag":                                         lo.ToPtr("my-tag"),
						"nodeclass-tag":                                    lo.ToPtr("nodeclass-value"),
					},
				},
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "my-tag",
			}
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}

			patchExists := inplaceupdate.CalculateAKSMachinePatch(options, nodeClaim, nodeClass, aksMachine)

			Expect(patchExists).To(BeFalse())

			// Verify original aksMachine tags remain unchanged
			Expect(aksMachine.Properties.Tags).To(HaveLen(6))
			Expect(aksMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(lo.ToPtr(opts.ClusterName)))
			Expect(aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_nodeclaim"]).To(Equal(lo.ToPtr(nodeClaim.Name)))
			Expect(aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"]).To(Equal(lo.ToPtr(createTimeString)))
			Expect(aksMachine.Properties.Tags["test-tag"]).To(Equal(lo.ToPtr("my-tag")))
			Expect(aksMachine.Properties.Tags["nodeclass-tag"]).To(Equal(lo.ToPtr("nodeclass-value")))
		})

		It("should handle AKS machine with nil properties", func() {
			aksMachine := &armcontainerservice.Machine{
				Properties: nil,
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "my-tag",
			}

			patchExists := inplaceupdate.CalculateAKSMachinePatch(options, nodeClaim, nodeClass, aksMachine)

			Expect(patchExists).To(BeTrue())

			// Verify original aksMachine properties were created and populated (expected behavior for AKS machines - PUT only, not PATCH)
			Expect(aksMachine.Properties).ToNot(BeNil())
			Expect(aksMachine.Properties.Tags).To(HaveKey("test-tag"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
		})

		It("should replace existing tags with expected tags", func() {
			aksMachine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{
						"karpenter.azure.com_cluster":                      lo.ToPtr(opts.ClusterName),
						"karpenter.azure.com_aksmachine_nodeclaim":         lo.ToPtr(nodeClaim.Name),
						"karpenter.azure.com_aksmachine_creationtimestamp": lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp())),
						"old-tag": lo.ToPtr("old-value"),
					},
				},
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"new-tag": "new-value",
			}

			patchExists := inplaceupdate.CalculateAKSMachinePatch(options, nodeClaim, nodeClass, aksMachine)

			Expect(patchExists).To(BeTrue())

			// Verify original aksMachine tags were replaced (expected behavior for AKS machines - PUT only, not PATCH)
			Expect(aksMachine.Properties.Tags).To(HaveKey("new-tag"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			Expect(aksMachine.Properties.Tags).ToNot(HaveKey("old-tag"))
		})

		It("should prioritize NodeClass tags over AdditionalTags", func() {
			aksMachine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{},
				},
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"conflict-tag": "additional-value",
			}
			nodeClass.Spec.Tags = map[string]string{
				"conflict-tag": "nodeclass-value",
			}

			patchExists := inplaceupdate.CalculateAKSMachinePatch(options, nodeClaim, nodeClass, aksMachine)

			Expect(patchExists).To(BeTrue())

			// Verify original aksMachine was modified with correct priority (expected behavior for AKS machines - PUT only, not PATCH)
			Expect(aksMachine.Properties.Tags["conflict-tag"]).To(Equal(lo.ToPtr("nodeclass-value")))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(aksMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
		})
	})

	Context("CalculateHash", func() {
		It("should produce deterministic hashes for identical data", func() {
			data := map[string]string{
				"key1": "value1",
				"key2": "value2",
			}

			hash1, err := inplaceupdate.CalculateHash(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash1).ToNot(BeEmpty())

			hash2, err := inplaceupdate.CalculateHash(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash2).ToNot(BeEmpty())

			Expect(hash1).To(Equal(hash2))
		})

		It("should produce different hashes for different data", func() {
			data1 := map[string]string{
				"key1": "value1",
				"key2": "value2",
			}

			data2 := map[string]string{
				"key1": "value1",
				"key2": "different_value",
			}

			hash1, err := inplaceupdate.CalculateHash(data1)
			Expect(err).ToNot(HaveOccurred())

			hash2, err := inplaceupdate.CalculateHash(data2)
			Expect(err).ToNot(HaveOccurred())

			Expect(hash1).ToNot(Equal(hash2))
		})

		It("should handle nil interface{}", func() {
			var data interface{}
			hash, err := inplaceupdate.CalculateHash(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).ToNot(BeEmpty())

			// Verify it's deterministic by computing again
			hash2, err := inplaceupdate.CalculateHash(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).To(Equal(hash2))
		})

		It("should handle empty types", func() {
			By("handling empty map")
			emptyMap := map[string]string{}
			hash, err := inplaceupdate.CalculateHash(emptyMap)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).ToNot(BeEmpty())

			By("handling empty slice")
			emptySlice := []string{}
			hash, err = inplaceupdate.CalculateHash(emptySlice)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).ToNot(BeEmpty())

			By("handling nil slice")
			var nilSlice []string
			hash, err = inplaceupdate.CalculateHash(nilSlice)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).ToNot(BeEmpty())
		})

		It("should handle primitive types", func() {
			primitives := []interface{}{
				"test-string",
				42,
				true,
				3.14,
			}

			for _, data := range primitives {
				hash, err := inplaceupdate.CalculateHash(data)
				Expect(err).ToNot(HaveOccurred())
				Expect(hash).ToNot(BeEmpty())
			}
		})

		It("should handle struct types", func() {
			type testStruct struct {
				Name  string            `json:"name"`
				Tags  map[string]string `json:"tags,omitempty"`
				Count int               `json:"count"`
			}

			data := testStruct{
				Name: "test",
				Tags: map[string]string{
					"env": "test",
				},
				Count: 5,
			}

			hash, err := inplaceupdate.CalculateHash(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).ToNot(BeEmpty())

			// Test that identical structs produce same hash
			data2 := testStruct{
				Name: "test",
				Tags: map[string]string{
					"env": "test",
				},
				Count: 5,
			}

			hash2, err := inplaceupdate.CalculateHash(data2)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).To(Equal(hash2))
		})

		It("should handle map key ordering", func() {
			// JSON marshaling is deterministic for maps in Go
			data1 := map[string]string{
				"b": "value2",
				"a": "value1",
				"c": "value3",
			}

			data2 := map[string]string{
				"a": "value1",
				"b": "value2",
				"c": "value3",
			}

			hash1, err := inplaceupdate.CalculateHash(data1)
			Expect(err).ToNot(HaveOccurred())

			hash2, err := inplaceupdate.CalculateHash(data2)
			Expect(err).ToNot(HaveOccurred())

			Expect(hash1).To(Equal(hash2))
		})

		It("should be sensitive to type differences", func() {
			// String vs int with same value
			hash1, err := inplaceupdate.CalculateHash("42")
			Expect(err).ToNot(HaveOccurred())

			hash2, err := inplaceupdate.CalculateHash(42)
			Expect(err).ToNot(HaveOccurred())

			Expect(hash1).ToNot(Equal(hash2))
		})

		It("should return numeric string format", func() {
			data := "test"
			hash, err := inplaceupdate.CalculateHash(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(hash).ToNot(BeEmpty())

			// Verify it's a numeric string (can be parsed as uint64)
			Expect(hash).To(MatchRegexp(`^\d+$`))
		})

		It("should handle unmarshalable types gracefully", func() {
			// Create a type that cannot be marshaled to JSON
			type unmarshalableStruct struct {
				Func func() // functions cannot be marshaled to JSON
			}

			data := unmarshalableStruct{
				Func: func() {},
			}

			hash, err := inplaceupdate.CalculateHash(data)
			Expect(err).To(HaveOccurred())
			Expect(hash).To(BeEmpty())
			Expect(err.Error()).To(ContainSubstring("json: unsupported type"))
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

		It("should add missing default tags", func() {
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
					"compute.aks.billing":         lo.ToPtr("linux"),          // Should always be included
					"test-tag":                    lo.ToPtr("my-tag"),
				},
			}))
		})

		It("should add missing tags from AdditionalTags", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				"compute.aks.billing":         lo.ToPtr("linux"),
			}

			options := test.Options()
			options.AdditionalTags = map[string]string{
				"test-tag": "my-tag",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
					"compute.aks.billing":         lo.ToPtr("linux"),          // Should always be included
					"test-tag":                    lo.ToPtr("my-tag"),
				},
			}))
		})

		It("should add missing tags from NodeClass", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				"compute.aks.billing":         lo.ToPtr("linux"),
			}

			options := test.Options()
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
					"compute.aks.billing":         lo.ToPtr("linux"),          // Should always be included
					"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
				},
			}))
		})

		It("should add missing tags from NodeClass if conflicting with AdditionalTags", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				"compute.aks.billing":         lo.ToPtr("linux"),
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
					"compute.aks.billing":         lo.ToPtr("linux"),          // Should always be included
					"test-tag":                    lo.ToPtr("nodeclass-value"),
				},
			}))
		})

		// NOTE: It is expected that this will remove manually added user tags as well
		It("should remove unneeded tags", func() {
			currentVM.Tags = map[string]*string{
				"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				"compute.aks.billing":         lo.ToPtr("linux"),
				"test-tag":                    lo.ToPtr("my-tag"),
			}

			options := test.Options()
			update := inplaceupdate.CalculateVMPatch(options, nodeClaim, nodeClass, currentVM)

			Expect(update).To(Equal(&armcompute.VirtualMachineUpdate{
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName), // Should always be included
					"compute.aks.billing":         lo.ToPtr("linux"),          // Should always be included
				},
			}))
		})
	})
})

