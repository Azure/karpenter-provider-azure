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

package status_test

import (
	"context"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/controllers/nodeclass/status"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
)

const (
	testCRGSubscriptionID = "12345678-1234-1234-1234-123456789012"
	testCRGLocation       = "eastus"
	testCRGResourceGroup  = "test-resourceGroup"
	testCRGName           = "test-crg"
)

func testCRGID() string {
	return "/subscriptions/" + testCRGSubscriptionID +
		"/resourceGroups/" + testCRGResourceGroup +
		"/providers/Microsoft.Compute/capacityReservationGroups/" + testCRGName
}

var _ = Describe("CapacityReservationGroupStatus", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var reconciler *status.CapacityReservationGroupReconciler

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(testCRGID())
		reconciler = status.NewCapacityReservationGroupReconciler(
			azureEnv.CapacityReservationGroupsAPI,
			azureEnv.CapacityReservationsAPI,
			testCRGSubscriptionID,
			testCRGLocation,
		)
	})

	expectUnready := func(reason string) {
		cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)
		Expect(cond.IsFalse()).To(BeTrue())
		Expect(cond.Reason).To(Equal(reason))
		Expect(nodeClass.Status.CapacityReservationGroup).To(BeNil())
	}

	It("should resolve the group and its member reservations", func() {
		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())

		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		crg := nodeClass.Status.CapacityReservationGroup
		Expect(crg).ToNot(BeNil())
		Expect(crg.Location).To(Equal(testCRGLocation))
		Expect(crg.Zones).To(ConsistOf("1", "2"))
		Expect(crg.CapacityReservations).To(HaveLen(1))
		Expect(crg.CapacityReservations[0].VMSize).To(Equal("Standard_D2s_v3"))
		Expect(crg.CapacityReservations[0].Zones).To(ConsistOf("1"))
		Expect(lo.FromPtr(crg.CapacityReservations[0].Quantity)).To(Equal(int32(1)))
	})

	// Azure accepts a zero-quantity reservation; it can be associated and then
	// intentionally overallocated, so it must not be treated as unusable.
	It("should accept a zero-quantity reservation", func() {
		azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
			return []*armcompute.CapacityReservation{
				fake.NewCapacityReservation(rg, group, "zero", "Standard_D2s_v3", 0, "1"),
			}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		Expect(lo.FromPtr(nodeClass.Status.CapacityReservationGroup.CapacityReservations[0].Quantity)).To(Equal(int32(0)))
	})

	It("should record each member's provisioning state", func() {
		azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
			creating := fake.NewCapacityReservation(rg, group, "creating", "Standard_D4s_v3", 1, "2")
			creating.Properties.ProvisioningState = lo.ToPtr("Creating")
			return []*armcompute.CapacityReservation{
				fake.NewCapacityReservation(rg, group, "ready", "Standard_D2s_v3", 1, "1"),
				creating,
			}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())

		// The unprovisioned member stays listed: an operator authors a NodePool per member
		// and needs to see why one of them stopped placing.
		members := nodeClass.Status.CapacityReservationGroup.CapacityReservations
		Expect(members).To(HaveLen(2))
		byName := lo.SliceToMap(members, func(m v1beta1.CapacityReservation) (string, v1beta1.CapacityReservation) { return m.Name, m })
		Expect(lo.FromPtr(byName["ready"].ProvisioningState)).To(Equal("Succeeded"))
		Expect(byName["ready"].IsEligible()).To(BeTrue())
		Expect(lo.FromPtr(byName["creating"].ProvisioningState)).To(Equal("Creating"))
		Expect(byName["creating"].IsEligible()).To(BeFalse())
	})

	// Otherwise the NodeClass reports Ready while projecting nothing, which reaches the
	// user as unschedulable pods rather than as a NodeClass problem.
	It("should reject a group whose members are all unprovisioned", func() {
		azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
			creating := fake.NewCapacityReservation(rg, group, "creating", "Standard_D2s_v3", 1, "1")
			creating.Properties.ProvisioningState = lo.ToPtr("Creating")
			return []*armcompute.CapacityReservation{creating}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsFalse()).To(BeTrue())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).Reason).
			To(Equal(status.CapacityReservationGroupUnreadyReasonNoEligibleReservations))

		// The members have to survive into status: this is the moment the operator needs
		// their names and states, because a NodePool was authored against each of them.
		crg := nodeClass.Status.CapacityReservationGroup
		Expect(crg).ToNot(BeNil(), "resolved group must remain visible when no member is eligible")
		Expect(crg.CapacityReservations).To(HaveLen(1))
		Expect(crg.CapacityReservations[0].Name).To(Equal("creating"))
		Expect(lo.FromPtr(crg.CapacityReservations[0].ProvisioningState)).To(Equal("Creating"))
	})

	// ARM documents provisioningState as always present in the response, so an absent
	// value is treated as usable rather than turning an optional field into an outage.
	It("should treat a member with no provisioning state as eligible", func() {
		azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
			bare := fake.NewCapacityReservation(rg, group, "bare", "Standard_D2s_v3", 1, "1")
			bare.Properties = nil
			return []*armcompute.CapacityReservation{bare}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		Expect(nodeClass.Status.CapacityReservationGroup.CapacityReservations[0].ProvisioningState).To(BeNil())
	})

	It("should resolve a regional group with no zones", func() {
		azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
			return armcompute.CapacityReservationGroupsClientGetResponse{
				CapacityReservationGroup: armcompute.CapacityReservationGroup{
					ID:       lo.ToPtr(testCRGID()),
					Name:     lo.ToPtr(name),
					Location: lo.ToPtr(testCRGLocation),
				},
			}, nil
		}
		azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
			return []*armcompute.CapacityReservation{
				fake.NewCapacityReservation(rg, group, "regional", "Standard_D2s_v3", 3),
			}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.Status.CapacityReservationGroup.Zones).To(BeEmpty())
		Expect(nodeClass.Status.CapacityReservationGroup.CapacityReservations[0].Zones).To(BeEmpty())
	})

	// ARM returns resource IDs with inconsistent casing, so comparisons against the
	// configured subscription must be case-insensitive.
	It("should tolerate mixed-case resource IDs", func() {
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(
			"/SUBSCRIPTIONS/" + testCRGSubscriptionID +
				"/RESOURCEGROUPS/" + testCRGResourceGroup +
				"/PROVIDERS/MICROSOFT.COMPUTE/CAPACITYRESERVATIONGROUPS/" + testCRGName)

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
	})

	It("should do nothing when no group is configured", func() {
		nodeClass.Spec.CapacityReservationGroupID = nil

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.Status.CapacityReservationGroup).To(BeNil())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)).To(BeNil())
	})

	It("should fail closed in AKS Machine API provisioning mode", func() {
		machinesCtx := options.ToContext(ctx, test.Options(test.OptionsFields{
			ProvisionMode: lo.ToPtr(consts.ProvisionModeAKSMachineAPI),
		}))

		_, err := reconciler.Reconcile(machinesCtx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonUnsupportedProvisionMode)
	})

	It("should reject a malformed resource ID", func() {
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr("not-a-resource-id")

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonIDInvalid)
	})

	It("should reject an ID for a different resource type", func() {
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(
			"/subscriptions/" + testCRGSubscriptionID +
				"/resourceGroups/" + testCRGResourceGroup +
				"/providers/Microsoft.Compute/diskEncryptionSets/foo")

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonIDInvalid)
	})

	It("should reject a group in another subscription", func() {
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(
			"/subscriptions/00000000-0000-0000-0000-000000000000" +
				"/resourceGroups/" + testCRGResourceGroup +
				"/providers/Microsoft.Compute/capacityReservationGroups/" + testCRGName)

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonSubscriptionMismatch)
	})

	It("should reject a group in another region", func() {
		azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
			return armcompute.CapacityReservationGroupsClientGetResponse{
				CapacityReservationGroup: armcompute.CapacityReservationGroup{
					ID:       lo.ToPtr(testCRGID()),
					Location: lo.ToPtr("westus2"),
				},
			}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonRegionMismatch)
	})

	// Azure omits reservationType for Targeted groups, so only an explicit
	// non-Targeted value is a rejection.
	It("should reject a Block reservation group", func() {
		azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
			return armcompute.CapacityReservationGroupsClientGetResponse{
				CapacityReservationGroup: armcompute.CapacityReservationGroup{
					ID:       lo.ToPtr(testCRGID()),
					Location: lo.ToPtr(testCRGLocation),
					Properties: &armcompute.CapacityReservationGroupProperties{
						ReservationType: lo.ToPtr(armcompute.ReservationTypeBlock),
					},
				},
			}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonUnsupportedType)
	})

	It("should accept a group with an omitted reservation type", func() {
		azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
			return armcompute.CapacityReservationGroupsClientGetResponse{
				CapacityReservationGroup: armcompute.CapacityReservationGroup{
					ID:         lo.ToPtr(testCRGID()),
					Location:   lo.ToPtr(testCRGLocation),
					Properties: &armcompute.CapacityReservationGroupProperties{},
				},
			}, nil
		}

		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
	})

	// AKS associates a node pool with a warning when the group has no reservation.
	// We fail closed instead, because an empty group can never back an offering.
	It("should reject a group with no member reservations", func() {
		azureEnv.CapacityReservationsAPI.ListFunc = func(_, _ string) ([]*armcompute.CapacityReservation, error) {
			return nil, nil
		}

		result, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.RequeueAfter).ToNot(BeZero())
		expectUnready(status.CapacityReservationGroupUnreadyReasonNoReservations)
	})

	It("should report not found", func() {
		azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
			return armcompute.CapacityReservationGroupsClientGetResponse{}, &azcore.ResponseError{
				ErrorCode:   "ResourceNotFound",
				StatusCode:  http.StatusNotFound,
				RawResponse: &http.Response{StatusCode: http.StatusNotFound},
			}
		}

		result, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))
		expectUnready(status.CapacityReservationGroupUnreadyReasonNotFound)
	})

	// Readiness can only prove read access. Permission to associate an instance with
	// the group is not observable until launch.
	It("should report access denied", func() {
		azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
			return armcompute.CapacityReservationGroupsClientGetResponse{}, &azcore.ResponseError{
				ErrorCode:   "AuthorizationFailed",
				StatusCode:  http.StatusForbidden,
				RawResponse: &http.Response{StatusCode: http.StatusForbidden},
			}
		}

		result, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))
		expectUnready(status.CapacityReservationGroupUnreadyReasonAccessDenied)
	})

	It("should clear resolved status when the group becomes unusable", func() {
		_, err := reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeClass.Status.CapacityReservationGroup).ToNot(BeNil())

		azureEnv.CapacityReservationsAPI.ListFunc = func(_, _ string) ([]*armcompute.CapacityReservation, error) {
			return nil, nil
		}
		_, err = reconciler.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonNoReservations)
	})
})
