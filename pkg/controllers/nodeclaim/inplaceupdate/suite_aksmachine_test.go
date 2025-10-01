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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v7"
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
	"github.com/Azure/karpenter-provider-azure/pkg/utils"
)

var _ = Describe("In Place Update Controller", func() {
	Context("AKS machine instances", func() {
		var aksMachine *armcontainerservice.Machine
		var nodeClaim *karpv1.NodeClaim
		var nodeClass *v1beta1.AKSNodeClass

		BeforeEach(func() {
			nodeClaimName := test.RandomName("nodeclaim")
			aksMachine = test.AKSMachine(test.AKSMachineOptions{
				ClusterName:      opts.ClusterName,
				MachinesPoolName: opts.AKSMachinesPoolName,
				Properties: &armcontainerservice.MachineProperties{
					Tags: map[string]*string{
						"karpenter.azure.com_cluster":                      lo.ToPtr(opts.ClusterName),
						"karpenter.azure.com_aksmachine_nodeclaim":         lo.ToPtr(nodeClaimName),
						"karpenter.azure.com_aksmachine_creationtimestamp": lo.ToPtr(instance.AKSMachineTimestampToTag(instance.NewAKSMachineTimestamp())),
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

			ctx = options.ToContext(ctx, test.Options())

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

		It("should not call Azure if the hash matches", func() {
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
			hash, err := inplaceupdate.HashFromNodeClaim(opts, nodeClaim, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			// Force the goal hash into annotations here, which should prevent the reconciler from doing anything on Azure
			nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash] = hash

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(0))

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
			Expect(azureEnv.AKSMachinesAPI.AKSMachineCreateOrUpdateBehavior.Calls()).To(Equal(0))
		})

		It("should handle invalid ProviderID gracefully", func() {
			nodeClaim.Status.ProviderID = "invalid-provider-id"

			ExpectApplied(ctx, env.Client, nodeClaim)
			result, err := inPlaceUpdateController.Reconcile(ctx, nodeClaim)

			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should handle AKS machine not found gracefully", func() {
			// Don't store the AKS machine in the fake API, so Get will fail

			ExpectApplied(ctx, env.Client, nodeClaim)
			result, err := inPlaceUpdateController.Reconcile(ctx, nodeClaim)

			Expect(err).To(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should add a hash annotation to NodeClaim if there are no tags", func() {
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ExpectApplied(ctx, env.Client, nodeClaim)
			ExpectObjectReconciled(ctx, env.Client, inPlaceUpdateController, nodeClaim)

			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedAKSMachine).To(Equal(aksMachine)) // No change expected

			// The nodeClaim should have the InPlaceUpdateHash annotation
			nodeClaim = ExpectExists(ctx, env.Client, nodeClaim)
			Expect(nodeClaim.Annotations).To(HaveKey(v1beta1.AnnotationInPlaceUpdateHash))
			Expect(nodeClaim.Annotations[v1beta1.AnnotationInPlaceUpdateHash]).ToNot(BeEmpty())
		})

		It("should add a hash annotation to NodeClaim and update AKS machine if there are missing tags", func() {
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)
			originalTags := aksMachine.Properties.Tags

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

			updatedAKSMachine, err := azureEnv.AKSMachineProvider.Get(ctx, *aksMachine.Name)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedAKSMachine.Properties.Tags).ToNot(Equal(originalTags))
			Expect(updatedAKSMachine.Properties.Tags).To(HaveKey("karpenter.azure.com_cluster"))
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
		})

		It("should successfully update AKS machine with correct ETag", func() {
			// Setup machine with ETag
			aksMachine.Properties.ETag = lo.ToPtr(`"valid-etag"`)
			azureEnv.AKSDataStorage.AKSMachines.Store(lo.FromPtr(aksMachine.ID), *aksMachine)

			ctx = options.ToContext(
				ctx,
				test.Options(
					test.OptionsFields{
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

			// Verify ETag was updated after successful operation
			Expect(updatedAKSMachine.Properties.ETag).ToNot(BeNil())
			Expect(*updatedAKSMachine.Properties.ETag).ToNot(Equal("valid-etag"))

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
})
