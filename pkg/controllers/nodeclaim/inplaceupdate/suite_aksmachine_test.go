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
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/awslabs/operatorpkg/object"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclaim/inplaceupdate"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "github.com/Azure/karpenter-provider-azure/pkg/test/expectations"
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

// inPlaceUpdateTestMode provides mode-specific operations for shared in-place update controller tests.
// Both VM and AKS machine contexts provide their own implementation, so the common test logic
// can run against either instance type without duplication.
type inPlaceUpdateTestMode struct {
	getNodeClaim             func() *karpv1.NodeClaim
	getNodeClass             func() *v1beta1.AKSNodeClass
	storeInstance            func()
	getUpdateCalls           func() int
	assertInstanceUnchanged func() // verifies the stored instance was not modified
}

// runSharedInPlaceUpdateBasicTests generates the basic controller tests that are identical
// across VM and AKS machine modes. Each mode's BeforeEach sets up its own instance type;
// these shared tests exercise the common reconciliation paths.
func runSharedInPlaceUpdateBasicTests(mode func() inPlaceUpdateTestMode) {
	It("should not call Azure if the hash matches", func() {
		m := mode()
		m.storeInstance()
		nc := m.getNodeClaim()
		hash, err := inplaceupdate.HashFromNodeClaim(opts, nc, m.getNodeClass())
		Expect(err).ToNot(HaveOccurred())

		if nc.Annotations == nil {
			nc.Annotations = map[string]string{}
		}
		nc.Annotations[v1beta1.AnnotationInPlaceUpdateHash] = hash

		ExpectApplied(ctx, env.Client, nc)
		ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nc)

		Expect(m.getUpdateCalls()).To(Equal(0))

		nc = ExpectExists(ctx, env.Client, nc)
		Expect(nc.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
		Expect(nc.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
	})

	It("should skip reconciliation when NodeClaim has no ProviderID", func() {
		m := mode()
		nc := m.getNodeClaim()
		nc.Status.ProviderID = ""

		ExpectApplied(ctx, env.Client, nc)
		result, err := inPlaceUpdateController.Reconcile(ctx, nc)

		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{RequeueAfter: 60 * time.Second}))
		Expect(m.getUpdateCalls()).To(Equal(0))
	})

	It("should handle invalid ProviderID gracefully", func() {
		m := mode()
		nc := m.getNodeClaim()
		nc.Status.ProviderID = "invalid-provider-id"

		ExpectApplied(ctx, env.Client, nc)
		result, err := inPlaceUpdateController.Reconcile(ctx, nc)

		Expect(err).To(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("should handle instance not found gracefully", func() {
		m := mode()
		// Don't store the instance in the fake API, so Get will fail
		nc := m.getNodeClaim()

		ExpectApplied(ctx, env.Client, nc)
		result, err := inPlaceUpdateController.Reconcile(ctx, nc)

		Expect(err).To(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
		Expect(err.Error()).To(ContainSubstring("nodeclaim not found"))
	})

	It("should add a hash annotation to NodeClaim if there are no config changes", func() {
		m := mode()
		m.storeInstance()
		nc := m.getNodeClaim()

		ExpectApplied(ctx, env.Client, nc)
		ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nc)

		nc = ExpectExists(ctx, env.Client, nc)
		Expect(nc.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
		Expect(nc.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())

		// Verify the underlying instance was NOT modified (no unnecessary Azure API calls)
		Expect(m.getUpdateCalls()).To(Equal(0))
		m.assertInstanceUnchanged()
	})
}

var _ = Describe("In Place Update Controller", func() {
	Context("AKS machine instances", func() {
		var aksMachine *armcontainerservice.Machine
		var nodeClaim *karpv1.NodeClaim
		var nodeClass *v1beta1.AKSNodeClass

		BeforeEach(func() {
			// Enable AKS machines management for these tests
			ctx = options.ToContext(ctx, test.Options(test.OptionsFields{
				ManageExistingAKSMachines: lo.ToPtr(true),
			}))

			nodeClaimName := test.RandomName("nodeclaim")
			aksMachine = test.AKSMachine(test.AKSMachineOptions{
				ClusterName:      opts.ClusterName,
				MachinesPoolName: opts.AKSMachinesPoolName,
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{
						"karpenter.azure.com_cluster":                      lo.ToPtr(opts.ClusterName),
						"karpenter.azure.com_aksmachine_nodeclaim":         lo.ToPtr(nodeClaimName),
						"karpenter.azure.com_aksmachine_creationtimestamp": lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp())),
						"compute.aks.billing":                              lo.ToPtr("linux"),
					},
				},
			})

			// Create a test AKSNodeClass that the NodeClaim can reference
			nodeClass = test.AKSNodeClass()
			nodeClaim = coretest.NodeClaim(karpv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeClaimName,
				},
				Spec: karpv1.NodeClaimSpec{
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
				},
				Status: karpv1.NodeClaimStatus{
					ProviderID: utils.VMResourceIDToProviderID(ctx, lo.FromPtr(aksMachine.Properties.ResourceID)),
				},
			})
			// Add AKS machine annotation to identify this as an AKS machine instance
			nodeClaim.Annotations = map[string]string{
				v1beta1.AnnotationAKSMachineResourceID: fake.MkMachineID(azureEnv.AzureResourceGraphAPI.ResourceGroup, opts.ClusterName, opts.AKSMachinesPoolName, *aksMachine.Name),
			}
			// Claims are launched and registered by default
			nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
			nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)

			// Apply the nodeClass to the test environment
			ExpectApplied(ctx, env.Client, nodeClass)

			azureEnv.Reset()

			aksMachinesPool := test.AKSAgentPool(test.AKSAgentPoolOptions{
				ClusterName: opts.ClusterName,
				Name:        opts.AKSMachinesPoolName,
			})

			azureEnv.AKSDataStorage.AgentPools.Store(lo.FromPtr(aksMachinesPool.ID), *aksMachinesPool)
		})

		AfterEach(func() {
			ExpectCleanedUp(ctx, env.Client)
		})

		// Shared basic tests (hash match, no ProviderID, invalid ProviderID, not found, hash annotation)
		runSharedInPlaceUpdateBasicTests(func() inPlaceUpdateTestMode {
			return inPlaceUpdateTestMode{
				getNodeClaim:   func() *karpv1.NodeClaim { return nodeClaim },
				getNodeClass:   func() *v1beta1.AKSNodeClass { return nodeClass },
				storeInstance:  func() { azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine) },
				getUpdateCalls: func() int { return azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls() },
				assertInstanceUnchanged: func() {
					gotInstance, ok := azureEnv.AKSDataStorage.AKSMachines.Load(lo.FromPtr(aksMachine.ID))
					Expect(ok).To(BeTrue())
					Expect(gotInstance).To(Equal(*aksMachine))
				},
			}
		})

		It("should add a hash annotation to NodeClaim and update AKS machine if there are missing tags", func() {
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
			originalTags := aksMachine.Properties.Tags

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						ManageExistingAKSMachines: lo.ToPtr(true),
						AdditionalTags: map[string]string{
							"test-tag": "my-tag",
						},
					}))
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}

			ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedAKSMachine.Properties.Tags).ToNot(Equal(originalTags))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("nodeclass-tag"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("test-tag"))

			Expect(updatedAKSMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(lo.ToPtr(opts.ClusterName)))
			Expect(updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_nodeclaim"]).To(Equal(lo.ToPtr(nodeClaim.Name)))
			Expect(updatedAKSMachine.Properties.Tags["nodeclass-tag"]).To(Equal(lo.ToPtr("nodeclass-value")))
			Expect(updatedAKSMachine.Properties.Tags["test-tag"]).To(Equal(lo.ToPtr("my-tag")))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should not call Azure if the expected tags already exist on the AKS machine", func() {
			aksMachine.Properties.Tags["test-tag"] = lo.ToPtr("my-tag")
			aksMachine.Properties.Tags["nodeclass-tag"] = lo.ToPtr("nodeclass-value")
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						ManageExistingAKSMachines: lo.ToPtr(true),
						AdditionalTags: map[string]string{
							"test-tag": "my-tag",
						},
					}))
			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}

			ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(0))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should set creation timestamp to minimum time when no existing timestamp tag exists", func() {
			// Remove the creation timestamp tag that was set by BeforeEach
			originalName := *aksMachine.Name
			delete(aksMachine.Properties.Tags, "karpenter.azure.com_aksmachine_creationtimestamp")
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			// Should have made an API call to add the timestamp tag
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(1))
			// Should not have made any delete calls
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.Calls()).To(Equal(0))

			// Verify the same machine was updated (not a new one created)
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			updatedAKSMachine := createInput.AKSMachine
			Expect(*updatedAKSMachine.Name).To(Equal(originalName))

			// Verify the timestamp was set to minimum time (1970-01-01T00:00:00.000Z)
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			timestampTag := updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"]
			Expect(*timestampTag).To(Equal("1970-01-01T00:00:00.00Z"))
		})

		It("should set creation timestamp to minimum time when existing timestamp tag is corrupt", func() {
			// Set a corrupt timestamp tag
			originalName := *aksMachine.Name
			aksMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"] = lo.ToPtr("invalid-timestamp-format")
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			// Should have made an API call to fix the timestamp tag
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(1))
			// Should not have made any delete calls
			Expect(azureEnv.AKSAgentPoolsAPI.AgentPoolDeleteMachinesBehavior.Calls()).To(Equal(0))

			// Verify the same machine was updated (not a new one created)
			createInput := azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.CalledWithInput.Pop()
			updatedAKSMachine := createInput.AKSMachine
			Expect(*updatedAKSMachine.Name).To(Equal(originalName))

			// Verify the timestamp was set to minimum time (1970-01-01T00:00:00.000Z)
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			timestampTag := updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_creationtimestamp"]
			Expect(*timestampTag).To(Equal("1970-01-01T00:00:00.00Z"))
		})

		It("should clear existing tags on AKS machine", func() {
			aksMachine.Properties.Tags = map[string]*string{
				"karpenter.azure.com_cluster":                      lo.ToPtr(opts.ClusterName),
				"karpenter.azure.com_aksmachine_nodeclaim":         lo.ToPtr(nodeClaim.Name),
				"karpenter.azure.com_aksmachine_creationtimestamp": lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp())),
				"test-tag":      lo.ToPtr("my-tag"),
				"nodeclass-tag": lo.ToPtr("nodeclass-value"),
			}
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			nodeClass.Spec.Tags = map[string]string{
				"nodeclass-tag": "nodeclass-value",
			}

			ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("nodeclass-tag"))
			Expect(updatedAKSMachine.Properties.Tags).ToNot(HaveKey("test-tag")) // should be removed

			Expect(updatedAKSMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(lo.ToPtr(opts.ClusterName)))
			Expect(updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_nodeclaim"]).To(Equal(lo.ToPtr(nodeClaim.Name)))
			Expect(updatedAKSMachine.Properties.Tags["nodeclass-tag"]).To(Equal(lo.ToPtr("nodeclass-value")))

			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should not apply tags if claim is not registered yet. Retry succeeds", func() {
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
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
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(0))

			// Now claim registers
			nodeClaim.StatusConditions().SetTrue(karpv1.ConditionTypeRegistered)
			ExpectApplied(ctx, env.Client, nodeClaim)

			// Retry should succeed
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)
			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())

			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("compute.aks.billing"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_nodeclaim"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_aksmachine_creationtimestamp"))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("nodeclass-tag"))

			Expect(updatedAKSMachine.Properties.Tags["karpenter.azure.com_cluster"]).To(Equal(lo.ToPtr(opts.ClusterName)))
			Expect(updatedAKSMachine.Properties.Tags["karpenter.azure.com_aksmachine_nodeclaim"]).To(Equal(lo.ToPtr(nodeClaim.Name)))
			Expect(updatedAKSMachine.Properties.Tags["nodeclass-tag"]).To(Equal(lo.ToPtr("nodeclass-value")))
		})

		It("should handle ETag mismatch during AKS machine update", func() {
			// Setup machine with ETag
			aksMachine.Properties.ETag = lo.ToPtr(`"initial-etag"`)
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						ManageExistingAKSMachines: lo.ToPtr(true),
						AdditionalTags: map[string]string{
							"test-tag": "my-tag",
						},
					}))

			ExpectApplied(ctx, env.Client, nodeClaim)

			// Set up a behavior that will fail on CreateOrUpdate with ETag mismatch
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Error.Set(&azcore.ResponseError{
				StatusCode: http.StatusPreconditionFailed,
				ErrorCode:  "ConditionNotMet",
			})

			// Controller should fail when trying to update due to ETag mismatch
			result, err := inPlaceUpdateController.Reconcile(ctx, nodeClaim)

			// Should fail due to ETag mismatch
			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			Expect(err.Error()).To(ContainSubstring("failed to apply update to AKS machine"))

			// Reset the error for other tests
			azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Error.Reset()

			// Try reconciling again.
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			// Verify update succeeded
			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			// Verify tags were applied
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("test-tag"))
			Expect(updatedAKSMachine.Properties.Tags["test-tag"]).To(Equal(lo.ToPtr("my-tag")))

			// Verify ETag was updated after successful operation (changed from original "initial-etag")
			Expect(updatedAKSMachine.Properties.ETag).ToNot(BeNil())
			Expect(*updatedAKSMachine.Properties.ETag).ToNot(Equal(`"initial-etag"`))

			// Verify API was called (once for the successful retry)
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(1))
		})

		It("should successfully update AKS machine with correct ETag", func() {
			// Setup machine with ETag
			aksMachine.Properties.ETag = lo.ToPtr(`"valid-etag"`)
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						ManageExistingAKSMachines: lo.ToPtr(true),
						AdditionalTags: map[string]string{
							"test-tag": "my-tag",
						},
					}))

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			// Verify update succeeded
			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			// Verify tags were applied
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("test-tag"))
			Expect(updatedAKSMachine.Properties.Tags["test-tag"]).To(Equal(lo.ToPtr("my-tag")))

			// Verify ETag was updated after successful operation (changed from original "valid-etag")
			Expect(updatedAKSMachine.Properties.ETag).ToNot(BeNil())
			Expect(*updatedAKSMachine.Properties.ETag).ToNot(Equal(`"valid-etag"`))

			// Verify API was called
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(1))
		})

		It("should handle missing ETag gracefully (backward compatibility)", func() {
			// Setup machine without ETag
			aksMachine.Properties.ETag = nil
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
						ManageExistingAKSMachines: lo.ToPtr(true),
						AdditionalTags: map[string]string{
							"test-tag": "my-tag",
						},
					}))

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			// Should succeed even without ETag
			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			// Verify tags were applied
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("test-tag"))
			Expect(updatedAKSMachine.Properties.Tags["test-tag"]).To(Equal(lo.ToPtr("my-tag")))

			// Verify API was called
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(1))
		})
	})

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
					"compute.aks.billing":         lo.ToPtr("linux"),
				},
			}
			nic = &armnetwork.Interface{
				ID:   lo.ToPtr(fake.MakeNetworkInterfaceID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName)),
				Name: lo.ToPtr(vmName),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
					"compute.aks.billing":         lo.ToPtr("linux"),
				},
			}
			billingExt = &armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "computeAksLinuxBilling")),
				Name: lo.ToPtr("computeAksLinuxBilling"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
					"compute.aks.billing":         lo.ToPtr("linux"),
				},
			}
			cseExt = &armcompute.VirtualMachineExtension{
				ID:   lo.ToPtr(fake.MakeVMExtensionID(azureEnv.AzureResourceGraphAPI.ResourceGroup, vmName, "cse-agent-karpenter")),
				Name: lo.ToPtr("cse-agent-karpenter"),
				Tags: map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
					"compute.aks.billing":         lo.ToPtr("linux"),
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

		// Shared basic tests (hash match, no ProviderID, invalid ProviderID, not found, hash annotation)
		runSharedInPlaceUpdateBasicTests(func() inPlaceUpdateTestMode {
			return inPlaceUpdateTestMode{
				getNodeClaim:   func() *karpv1.NodeClaim { return nodeClaim },
				getNodeClass:   func() *v1beta1.AKSNodeClass { return nodeClass },
				storeInstance:  func() { azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm) },
				getUpdateCalls: func() int { return azureEnv.VirtualMachinesAPI.VirtualMachineUpdateBehavior.Calls() },
				assertInstanceUnchanged: func() {
					updatedVM, err := azureEnv.VMInstanceProvider.Get(ctx, vmName)
					Expect(err).ToNot(HaveOccurred())
					Expect(updatedVM).To(Equal(vm))
				},
			}
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
					"compute.aks.billing":         lo.ToPtr("linux"),
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
					"compute.aks.billing":         lo.ToPtr("linux"),
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
						"compute.aks.billing":         lo.ToPtr("linux"),
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

			It("should add new billing tag if there are missing tags", func() {
				vm.Tags = map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				}
				azureEnv.VirtualMachinesAPI.Instances.Store(lo.FromPtr(vm.ID), *vm)
				nic.Tags = map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				}
				azureEnv.NetworkInterfacesAPI.NetworkInterfaces.Store(lo.FromPtr(nic.ID), *nic)
				billingExt.Tags = map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				}
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(billingExt.ID), *billingExt)
				cseExt.Tags = map[string]*string{
					"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
				}
				azureEnv.VirtualMachineExtensionsAPI.Extensions.Store(lo.FromPtr(cseExt.ID), *cseExt)

				ExpectApplied(ctx, env.Client, nodeClaim, nodeClass)
				ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

				// The VM should be updated
				updatedVM := ExpectInstanceResourcesHaveTags(
					ctx,
					vmName,
					azureEnv,
					map[string]*string{
						"karpenter.azure.com_cluster": lo.ToPtr(opts.ClusterName),
						"compute.aks.billing":         lo.ToPtr("linux"),
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
					"compute.aks.billing":         lo.ToPtr("linux"),
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
						"compute.aks.billing":         lo.ToPtr("linux"),
						"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
						// "test-tag" should be removed
					})
				// Expect the identities to remain unchanged
				Expect(updatedVM.Identity).To(BeNil())

				nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
				Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
				Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
			})

			DescribeTable(
				"should propagate update to tags from NodeClass",
				func(newTags map[string]string, expectedTags map[string]*string) {
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
				},
				Entry(
					"standard tags",
					map[string]string{"nodeclass-tag": "nodeclass-value"},
					map[string]*string{
						"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
						"compute.aks.billing":         lo.ToPtr("linux"),
						"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
					},
				),
				Entry(
					"tags with '/' should be replaced with '_'",
					map[string]string{"this/tag/has/slashes": "nodeclass-value"},
					map[string]*string{
						"karpenter.azure.com_cluster": lo.ToPtr("test-cluster"),
						"compute.aks.billing":         lo.ToPtr("linux"),
						"this_tag_has_slashes":        lo.ToPtr("nodeclass-value"),
					},
				),
			)

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
						"compute.aks.billing":         lo.ToPtr("linux"),
						"nodeclass-tag":               lo.ToPtr("nodeclass-value"),
					})
				Expect(updatedVM).ToNot(Equal(vm))
			})
		})
	})
})
