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
	"errors"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

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
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
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

// stubInstanceTypeLister stands in for the instance type projection so that most cases can
// exercise the reconciler without depending on the fake SKU catalog.
type stubInstanceTypeLister struct {
	called        bool
	received      *v1beta1.AKSNodeClass
	instanceTypes []*cloudprovider.InstanceType
	err           error
}

func (s *stubInstanceTypeLister) List(_ context.Context, nodeClass *v1beta1.AKSNodeClass) ([]*cloudprovider.InstanceType, error) {
	s.called = true
	s.received = nodeClass.DeepCopy()
	return s.instanceTypes, s.err
}

var _ = Describe("CapacityReservationGroupStatus", func() {
	var nodeClass *v1beta1.AKSNodeClass
	var reconciler *status.CapacityReservationGroupReconciler
	var lister *stubInstanceTypeLister

	BeforeEach(func() {
		nodeClass = test.AKSNodeClass()
		nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(testCRGID())
		lister = &stubInstanceTypeLister{instanceTypes: []*cloudprovider.InstanceType{{Name: "Standard_D2s_v3"}}}
		reconciler = status.NewCapacityReservationGroupReconciler(
			azureEnv.CapacityReservationGroupsAPI,
			azureEnv.CapacityReservationsAPI,
			lister,
			testCRGSubscriptionID,
			testCRGLocation,
			cloud.AzurePublic,
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

	Context("compatibility with the instance type projection", func() {
		It("should ask the projection about the status it is about to write", func() {
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			Expect(lister.called).To(BeTrue())
			// The prospective status, not the one the object had on entry.
			Expect(lister.received.Status.CapacityReservationGroup).ToNot(BeNil())
			Expect(lister.received.Status.CapacityReservationGroup.CapacityReservations).To(HaveLen(1))
			Expect(lister.received.Spec.CapacityReservationGroupID).To(Equal(nodeClass.Spec.CapacityReservationGroupID))
		})

		It("should be ready when the projection yields instance types", func() {
			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		})

		It("should report no compatible reservations when the projection yields nothing", func() {
			lister.instanceTypes = nil

			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal(status.CapacityReservationGroupUnreadyReasonNoCompatibleReservations))
			// Members stay listed: the operator has to see which sizes were reserved.
			Expect(nodeClass.Status.CapacityReservationGroup).ToNot(BeNil())
			Expect(nodeClass.Status.CapacityReservationGroup.CapacityReservations).To(HaveLen(1))
		})

		// A projection failure says nothing about the group, so it must be retried rather
		// than reported as the group being unusable.
		It("should retry a projection error instead of calling it incompatible", func() {
			lister.err = errors.New("instance types unavailable")

			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).To(HaveOccurred())

			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)
			Expect(cond.Reason).To(Equal(status.CapacityReservationGroupUnreadyReasonUnknownError))
			Expect(cond.Reason).ToNot(Equal(status.CapacityReservationGroupUnreadyReasonNoCompatibleReservations))
			Expect(nodeClass.Status.CapacityReservationGroup).ToNot(BeNil())
		})

		It("should not consult the projection when no member is eligible", func() {
			azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
				creating := fake.NewCapacityReservation(rg, group, "creating", "Standard_D2s_v3", 1, "1")
				creating.Properties.ProvisioningState = lo.ToPtr("Creating")
				return []*armcompute.CapacityReservation{creating}, nil
			}

			_, err := reconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(lister.called).To(BeFalse(), "eligibility must short-circuit before the projection")
		})
	})

	// Uses the real projection against the fake SKU catalog, so that compatibility stays
	// defined by one implementation rather than a restatement of it here.
	Context("compatibility against the real projection", func() {
		var realReconciler *status.CapacityReservationGroupReconciler

		regionalCRGID := "/subscriptions/" + testCRGSubscriptionID +
			"/resourceGroups/" + testCRGResourceGroup +
			"/providers/Microsoft.Compute/capacityReservationGroups/" + testCRGName

		BeforeEach(func() {
			nodeClass.Spec.CapacityReservationGroupID = lo.ToPtr(regionalCRGID)
			azureEnv.CapacityReservationGroupsAPI.GetFunc = func(_ context.Context, rg, name string, _ *armcompute.CapacityReservationGroupsClientGetOptions) (armcompute.CapacityReservationGroupsClientGetResponse, error) {
				return armcompute.CapacityReservationGroupsClientGetResponse{
					CapacityReservationGroup: armcompute.CapacityReservationGroup{
						ID:       lo.ToPtr(regionalCRGID),
						Name:     lo.ToPtr(name),
						Location: lo.ToPtr(fake.Region),
						Zones:    []*string{lo.ToPtr("1")},
					},
				}, nil
			}
			realReconciler = status.NewCapacityReservationGroupReconciler(
				azureEnv.CapacityReservationGroupsAPI,
				azureEnv.CapacityReservationsAPI,
				azureEnv.InstanceTypesProvider,
				testCRGSubscriptionID,
				fake.Region,
				cloud.AzurePublic,
			)
			Expect(azureEnv.InstanceTypesProvider.UpdateInstanceTypes(ctx)).To(Succeed())
		})

		It("should be ready for a size the region offers", func() {
			azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
				return []*armcompute.CapacityReservation{
					fake.NewCapacityReservation(rg, group, "ok", "Standard_D2s_v3", 1, "1"),
				}, nil
			}

			_, err := realReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		})

		It("should reject a size the region does not offer", func() {
			azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
				return []*armcompute.CapacityReservation{
					fake.NewCapacityReservation(rg, group, "absent", "Standard_NotARealSize_v9", 1, "1"),
				}, nil
			}

			_, err := realReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal(status.CapacityReservationGroupUnreadyReasonNoCompatibleReservations))
		})

		It("should reject a size the NodeClass filters out", func() {
			// LocalDNS needs at least four vCPUs, and the reserved size has two.
			nodeClass.Status.LocalDNSState = lo.ToPtr(v1beta1.LocalDNSStateEnabled)
			azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
				return []*armcompute.CapacityReservation{
					fake.NewCapacityReservation(rg, group, "small", "Standard_D2s_v3", 1, "1"),
				}, nil
			}

			_, err := realReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			cond := nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady)
			Expect(cond.IsFalse()).To(BeTrue())
			Expect(cond.Reason).To(Equal(status.CapacityReservationGroupUnreadyReasonNoCompatibleReservations))
		})

		// Readiness is about whether the shape is usable at all, not about whether capacity
		// happens to be available this minute.
		It("should stay ready when the reserved offering is temporarily unavailable", func() {
			azureEnv.CapacityReservationsAPI.ListFunc = func(rg, group string) ([]*armcompute.CapacityReservation, error) {
				return []*armcompute.CapacityReservation{
					fake.NewCapacityReservation(rg, group, "ok", "Standard_D2s_v3", 1, "1"),
				}, nil
			}
			azureEnv.UnavailableOfferingsCache.ForCapacityReservationGroup(regionalCRGID).MarkUnavailable(
				ctx, "ZonalAllocationFailure", fake.MakeSKU("Standard_D2s_v3"),
				fake.Region+"-1", "on-demand")

			_, err := realReconciler.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		})
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

	It("should fail closed in a cloud without capacity reservations", func() {
		// A cloud reachable only through a file-based environment; the known cloud names all
		// map to clouds that do support capacity reservations.
		stackCloud := cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: "https://management.contoso-stack.local/"},
		}}
		unsupported := status.NewCapacityReservationGroupReconciler(
			azureEnv.CapacityReservationGroupsAPI,
			azureEnv.CapacityReservationsAPI,
			lister,
			testCRGSubscriptionID,
			testCRGLocation,
			stackCloud,
		)

		_, err := unsupported.Reconcile(ctx, nodeClass)
		Expect(err).ToNot(HaveOccurred())
		expectUnready(status.CapacityReservationGroupUnreadyReasonUnsupportedCloud)
	})

	DescribeTable("should accept the clouds Azure documents",
		func(cfg cloud.Configuration) {
			supported := status.NewCapacityReservationGroupReconciler(
				azureEnv.CapacityReservationGroupsAPI,
				azureEnv.CapacityReservationsAPI,
				lister,
				testCRGSubscriptionID,
				testCRGLocation,
				cfg,
			)

			_, err := supported.Reconcile(ctx, nodeClass)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeClass.StatusConditions().Get(v1beta1.ConditionTypeCapacityReservationGroupReady).IsTrue()).To(BeTrue())
		},
		Entry("public", cloud.AzurePublic),
		Entry("government", cloud.AzureGovernment),
		Entry("china", cloud.AzureChina),
	)

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
		// Not the healthy interval: a group is commonly empty only while it is being
		// populated, and waiting a full revalidation period to notice strands the NodeClass.
		Expect(result).To(Equal(reconcile.Result{RequeueAfter: time.Minute}))
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
