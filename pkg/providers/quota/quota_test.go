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

package quota_test

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computelimit/armcomputelimit"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

func Test_Update_PopulatesUsageData(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, quotaCategoryVMFamilyMappingAPI, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(
		&armcompute.Usage{
			Name:         &armcompute.UsageName{Value: lo.ToPtr("standardBSFamily"), LocalizedValue: lo.ToPtr("Standard BS Family vCPUs")},
			CurrentValue: lo.ToPtr[int32](10),
			Limit:        lo.ToPtr[int64](100),
			Unit:         lo.ToPtr("Count"),
		},
		&armcompute.Usage{
			Name:         &armcompute.UsageName{Value: lo.ToPtr("cores"), LocalizedValue: lo.ToPtr("Total Regional vCPUs")},
			CurrentValue: lo.ToPtr[int32](50),
			Limit:        lo.ToPtr[int64](500),
			Unit:         lo.ToPtr("Count"),
		},
	)

	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, usage := quotaProvider.GetUsage("standardBSFamily")
	g.Expect(found).To(BeTrue())
	g.Expect(*usage.CurrentValue).To(Equal(int32(10)))
	g.Expect(*usage.Limit).To(Equal(int64(100)))
	// Shouldn't call the category API if general purpose isn't in the usages response
	g.Expect(quotaCategoryVMFamilyMappingAPI.Calls()).To(BeZero())
}

func Test_GetTotalRegionalUsage_ReturnsCoresUsage(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(
		&armcompute.Usage{
			Name:         &armcompute.UsageName{Value: lo.ToPtr("cores"), LocalizedValue: lo.ToPtr("Total Regional vCPUs")},
			CurrentValue: lo.ToPtr[int32](50),
			Limit:        lo.ToPtr[int64](500),
			Unit:         lo.ToPtr("Count"),
		},
	)

	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, usage := quotaProvider.GetTotalRegionalUsage()
	g.Expect(found).To(BeTrue())
	g.Expect(*usage.CurrentValue).To(Equal(int32(50)))
	g.Expect(*usage.Limit).To(Equal(int64(500)))
}

func Test_GetUsage_ReturnsFalseForUnknownFamily(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	_, _, quotaProvider := newTestProvider(t)

	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, _ := quotaProvider.GetUsage("nonExistentFamily")
	g.Expect(found).To(BeFalse())
}

func Test_GetTotalRegionalUsage_ReturnsFalseWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	_, _, quotaProvider := newTestProvider(t)

	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, _ := quotaProvider.GetTotalRegionalUsage()
	g.Expect(found).To(BeFalse())
}

func Test_Update_PreservesCachedDataOnFailure(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(
		&armcompute.Usage{
			Name:         &armcompute.UsageName{Value: lo.ToPtr("cores"), LocalizedValue: lo.ToPtr("Total Regional vCPUs")},
			CurrentValue: lo.ToPtr[int32](50),
			Limit:        lo.ToPtr[int64](500),
			Unit:         lo.ToPtr("Count"),
		},
	)

	// First update succeeds
	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, usage := quotaProvider.GetTotalRegionalUsage()
	g.Expect(found).To(BeTrue())
	g.Expect(*usage.CurrentValue).To(Equal(int32(50)))

	// Configure a failure
	usageAPI.Error = fmt.Errorf("simulated API failure")

	// Second update fails
	err = quotaProvider.Update(ctx)
	g.Expect(err).To(HaveOccurred())

	// Previous data should still be available
	found, usage = quotaProvider.GetTotalRegionalUsage()
	g.Expect(found).To(BeTrue())
	g.Expect(*usage.CurrentValue).To(Equal(int32(50)))
}

func Test_HasQuotaFor(t *testing.T) {
	t.Parallel()
	sku := fake.MakeSKU("Standard_D4s_v3") // 4 vCPUs, standardDSv3Family

	tests := []struct {
		name                    string
		usages                  []*armcompute.Usage
		quotaCategoryVMFamilies []*armcomputelimit.VMFamily
		update                  bool // whether to call Update before checking
		expected                bool
	}{
		{
			name: "allows when enough quota",
			usages: []*armcompute.Usage{{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr[int32](10),
				Limit:        lo.ToPtr[int64](100),
			}},
			update:   true,
			expected: true,
		},
		{
			name: "blocks when insufficient quota",
			usages: []*armcompute.Usage{{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr[int32](98),
				Limit:        lo.ToPtr[int64](100),
			}},
			update:   true,
			expected: false, // 100-98=2 remaining, SKU needs 4
		},
		{
			name: "allows exact fit",
			usages: []*armcompute.Usage{{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr[int32](96),
				Limit:        lo.ToPtr[int64](100),
			}},
			update:   true,
			expected: true, // 100-96=4 remaining, SKU needs exactly 4
		},
		{
			name: "fails open when family not found",
			usages: []*armcompute.Usage{{
				Name:         &armcompute.UsageName{Value: lo.ToPtr("standardBSFamily")},
				CurrentValue: lo.ToPtr[int32](100),
				Limit:        lo.ToPtr[int64](100),
			}},
			update:   true,
			expected: true,
		},
		{
			name: "fails open when no data",
			// No usages, no Update call
			update:   false,
			expected: true,
		},
		{
			name: "fails open when Limit is nil",
			usages: []*armcompute.Usage{{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: lo.ToPtr[int32](50),
				Limit:        nil,
			}},
			update:   true,
			expected: true,
		},
		{
			name: "fails open when CurrentValue is nil",
			usages: []*armcompute.Usage{{
				Name:         &armcompute.UsageName{Value: lo.ToPtr(sku.GetFamilyName())},
				CurrentValue: nil,
				Limit:        lo.ToPtr[int64](100),
			}},
			update:   true,
			expected: true,
		},
		{
			name: "blocks when category is exhausted even if family has quota",
			usages: []*armcompute.Usage{
				newUsage(sku.GetFamilyName(), 0),
				newUsage(quota.GeneralPurposeCategory, 98),
			},
			quotaCategoryVMFamilies: []*armcomputelimit.VMFamily{
				newVMFamily(sku.GetFamilyName()),
			},
			update:   true,
			expected: false,
		},
		{
			name: "allows when category has quota even if family is exhausted",
			usages: []*armcompute.Usage{
				newUsage(sku.GetFamilyName(), 100),
				newUsage(quota.GeneralPurposeCategory, 0),
			},
			quotaCategoryVMFamilies: []*armcomputelimit.VMFamily{
				newVMFamily(sku.GetFamilyName()),
			},
			update:   true,
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := TestContextWithLogger(t)
			g := NewWithT(t)
			usageAPI, quotaCategoryVMFamilyMappingAPI, quotaProvider := newTestProvider(t)

			if len(tc.usages) > 0 {
				usageAPI.Usages.Append(tc.usages...)
			}
			if len(tc.quotaCategoryVMFamilies) > 0 {
				quotaCategoryVMFamilyMappingAPI.VMFamilies.Append(tc.quotaCategoryVMFamilies...)
			}
			if tc.update {
				lo.Must0(quotaProvider.Update(ctx))
			}

			g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(Equal(tc.expected))
		})
	}
}

func Test_GetUsage_ReturnsMappedCategoryUsage(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	usageAPI, quotaCategoryVMFamilyMappingAPI, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(
		newUsage(sku.GetFamilyName(), 0),
		newUsage(quota.GeneralPurposeCategory, 98),
	)
	quotaCategoryVMFamilyMappingAPI.VMFamilies.Append(newVMFamily(sku.GetFamilyName()))
	g.Expect(quotaProvider.Update(ctx)).To(Succeed())

	found, usage := quotaProvider.GetUsage(sku.GetFamilyName())
	g.Expect(found).To(BeTrue())
	g.Expect(lo.FromPtr(usage.CurrentValue)).To(Equal(int32(98)))
}

func Test_Update_PreservesAtomicSnapshotWhenVMFamiliesFail(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	usageAPI, quotaCategoryVMFamilyMappingAPI, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(
		newUsage(sku.GetFamilyName(), 0),
		newUsage(quota.GeneralPurposeCategory, 98),
	)
	quotaCategoryVMFamilyMappingAPI.VMFamilies.Append(newVMFamily(sku.GetFamilyName()))
	g.Expect(quotaProvider.Update(ctx)).To(Succeed())
	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())

	usageAPI.Usages.Append(newUsage(quota.GeneralPurposeCategory, 0))
	quotaCategoryVMFamilyMappingAPI.VMFamilies.Append(newVMFamily("standardBSFamily"))
	quotaCategoryVMFamilyMappingAPI.PageSize = 1
	quotaCategoryVMFamilyMappingAPI.ErrorPage = 1
	quotaCategoryVMFamilyMappingAPI.Error = fmt.Errorf("simulated quota category VM family mapping API failure")
	g.Expect(quotaProvider.Update(ctx)).To(MatchError(ContainSubstring("simulated quota category VM family mapping API failure")))
	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())
}

func Test_HasQuotaFor_IgnoresCategoryMappingInFailedProvisioningState(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	usageAPI, quotaCategoryVMFamilyMappingAPI, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(
		newUsage(sku.GetFamilyName(), 0),
		newUsage(quota.GeneralPurposeCategory, 100),
	)
	family := newVMFamily(sku.GetFamilyName())
	family.Properties.ProvisioningState = lo.ToPtr(armcomputelimit.ResourceProvisioningStateFailed)
	quotaCategoryVMFamilyMappingAPI.VMFamilies.Append(family)

	g.Expect(quotaProvider.Update(ctx)).To(Succeed())
	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
}

func Test_Reset_InvalidatesQuotaDataAndSubsequentRecovery(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)

	usageAPI.Usages.Append(&armcompute.Usage{
		Name:         &armcompute.UsageName{Value: lo.ToPtr("standardDSv3Family")},
		CurrentValue: lo.ToPtr[int32](10),
		Limit:        lo.ToPtr[int64](100),
	})
	lo.Must0(quotaProvider.Update(ctx))

	quotaProvider.Reset()
	found, _ := quotaProvider.GetUsage("standardDSv3Family")
	g.Expect(found).To(BeFalse())

	quotaProvider.Reset()
	lo.Must0(quotaProvider.Update(ctx))
	found, _ = quotaProvider.GetUsage("standardDSv3Family")
	g.Expect(found).To(BeTrue())
}

func Test_SeqNumTracksEffectiveQuotaChanges(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	familyName := sku.GetFamilyName()
	vCPUs := lo.Must(sku.VCPU())
	nodeClaim := testNodeClaim("seqnum")

	initialSeqNum := quotaProvider.SeqNum()
	usageAPI.Usages.Append(newUsage(familyName, 96))
	g.Expect(quotaProvider.Update(ctx)).To(Succeed())
	g.Expect(quotaProvider.SeqNum()).To(BeNumerically(">", initialSeqNum))

	updateSeqNum := quotaProvider.SeqNum()
	g.Expect(quotaProvider.RecordCreate(quotaProvider.SnapshotGeneration(), nodeClaim, familyName, vCPUs)).To(BeTrue())
	g.Expect(quotaProvider.SeqNum()).To(BeNumerically(">", updateSeqNum))

	createSeqNum := quotaProvider.SeqNum()
	g.Expect(quotaProvider.RecordCreate(quotaProvider.SnapshotGeneration(), nodeClaim, familyName, vCPUs)).To(BeFalse())
	g.Expect(quotaProvider.SeqNum()).To(Equal(createSeqNum))

	g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
	g.Expect(quotaProvider.SeqNum()).To(BeNumerically(">", createSeqNum))

	deleteSeqNum := quotaProvider.SeqNum()
	g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse())
	g.Expect(quotaProvider.SeqNum()).To(Equal(deleteSeqNum))

	quotaProvider.Reset()
	g.Expect(quotaProvider.SeqNum()).To(BeNumerically(">", deleteSeqNum))
}

func Test_LocalAdjustments(t *testing.T) {
	t.Parallel()
	sku := fake.MakeSKU("Standard_D4s_v3")
	familyName := sku.GetFamilyName()
	vCPUs := lo.Must(sku.VCPU())

	t.Run("ignores adjustments before the first snapshot", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		_, _, quotaProvider := newTestProvider(t)

		g.Expect(quotaProvider.RecordCreate(0, testNodeClaim("create"), familyName, vCPUs)).To(BeFalse())
		g.Expect(quotaProvider.RecordDelete(testNodeClaim("delete"), familyName, vCPUs)).To(BeFalse())
	})

	t.Run("counts an accepted create only once", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		usageAPI, _, quotaProvider := newTestProvider(t)
		usageAPI.Usages.Append(newUsage(familyName, 96))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		generation := quotaProvider.SnapshotGeneration()
		nodeClaim := testNodeClaim("create")
		createSKU := fake.MakeSKU("Standard_D2s_v3")
		createVCPUs := lo.Must(createSKU.VCPU())
		g.Expect(createSKU.GetFamilyName()).To(Equal(familyName))

		g.Expect(quotaProvider.RecordCreate(generation, nodeClaim, familyName, createVCPUs)).To(BeTrue())
		g.Expect(quotaProvider.RecordCreate(generation, nodeClaim, familyName, createVCPUs)).To(BeFalse())
		g.Expect(quotaProvider.HasQuotaFor(ctx, createSKU)).To(BeTrue(), "one debit should leave an exact fit for another 2-vCPU VM")
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse(), "the accepted create should consume 2 vCPUs")
	})

	t.Run("delete cancels create", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		usageAPI, _, quotaProvider := newTestProvider(t)
		usageAPI.Usages.Append(newUsage(familyName, 96))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		nodeClaim := testNodeClaim("create-delete")

		g.Expect(quotaProvider.RecordCreate(quotaProvider.SnapshotGeneration(), nodeClaim, familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
	})

	t.Run("refresh clears adjustments and ignores stale create generations", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		usageAPI, _, quotaProvider := newTestProvider(t)
		usageAPI.Usages.Append(newUsage(familyName, 96))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		generation := quotaProvider.SnapshotGeneration()

		g.Expect(quotaProvider.RecordCreate(generation, testNodeClaim("current-create"), familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
		g.Expect(quotaProvider.RecordCreate(generation, testNodeClaim("stale-create"), familyName, vCPUs)).To(BeFalse())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue(), "the stale create must not be counted")
		g.Expect(quotaProvider.RecordCreate(quotaProvider.SnapshotGeneration(), testNodeClaim("next-create"), familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse(), "the current-generation create must be counted exactly once")
	})
}

func Test_DeleteTombstonesDeduplicateAcrossRefreshes(t *testing.T) {
	t.Parallel()
	sku := fake.MakeSKU("Standard_D4s_v3")
	familyName := sku.GetFamilyName()
	vCPUs := lo.Must(sku.VCPU())

	t.Run("deduplicates repeated deletes", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		fakeClock := clocktesting.NewFakeClock(time.Now())
		usageAPI, _, quotaProvider := newTestProviderWithClock(t, fakeClock)
		usageAPI.Usages.Append(newUsage(familyName, 100))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		nodeClaim := testNodeClaim("delete")

		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
	})

	t.Run("retains tombstones across refreshes", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		fakeClock := clocktesting.NewFakeClock(time.Now())
		usageAPI, _, quotaProvider := newTestProviderWithClock(t, fakeClock)
		usageAPI.Usages.Append(newUsage(familyName, 100))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		nodeClaim := testNodeClaim("delete")

		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse())
	})

	t.Run("reset clears tombstones", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		fakeClock := clocktesting.NewFakeClock(time.Now())
		usageAPI, _, quotaProvider := newTestProviderWithClock(t, fakeClock)
		usageAPI.Usages.Append(newUsage(familyName, 100))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		nodeClaim := testNodeClaim("delete")

		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
		quotaProvider.Reset()
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
	})

	t.Run("expired tombstone does not duplicate an active delete adjustment", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		fakeClock := clocktesting.NewFakeClock(time.Now())
		usageAPI, _, quotaProvider := newTestProviderWithClock(t, fakeClock)
		usageAPI.Usages.Append(newUsage(familyName, 100))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		nodeClaim := testNodeClaim("delete")

		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
		fakeClock.Step(quota.DeleteTombstoneTTL + time.Second)
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse())
		g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue())
	})

	t.Run("expired tombstone is treated as new after refresh clears the adjustment", func(t *testing.T) {
		t.Parallel()
		ctx := TestContextWithLogger(t)
		g := NewWithT(t)
		fakeClock := clocktesting.NewFakeClock(time.Now())
		usageAPI, _, quotaProvider := newTestProviderWithClock(t, fakeClock)
		usageAPI.Usages.Append(newUsage(familyName, 100))
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		nodeClaim := testNodeClaim("delete")

		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
		g.Expect(quotaProvider.Update(ctx)).To(Succeed())
		fakeClock.Step(quota.DeleteTombstoneTTL + time.Second)
		g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
	})
}

func Test_UpdatePreservesDeleteRecordedDuringFetch(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	familyName := sku.GetFamilyName()
	vCPUs := lo.Must(sku.VCPU())
	nodeClaim := testNodeClaim("delete-during-refresh")

	usageAPI.Usages.Append(newUsage(familyName, 100))
	g.Expect(quotaProvider.Update(ctx)).To(Succeed())

	fetchStarted := make(chan struct{})
	continueFetch := make(chan struct{})
	usageAPI.BeforeFetch = func() {
		close(fetchStarted)
		<-continueFetch
	}
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- quotaProvider.Update(ctx)
	}()

	<-fetchStarted
	g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
	close(continueFetch)
	g.Expect(<-updateDone).To(Succeed())

	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue(), "the delete recorded during the fetch must survive publication")
	g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse(), "the retained tombstone must deduplicate later observations")
}

func Test_UpdatePreservesCreateFollowedByDeleteDuringFetch(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	familyName := sku.GetFamilyName()
	vCPUs := lo.Must(sku.VCPU())
	nodeClaim := testNodeClaim("create-delete-during-refresh")

	usageAPI.Usages.Append(newUsage(familyName, 96))
	g.Expect(quotaProvider.Update(ctx)).To(Succeed())
	g.Expect(quotaProvider.RecordCreate(quotaProvider.SnapshotGeneration(), nodeClaim, familyName, vCPUs)).To(BeTrue())
	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeFalse())

	// The refresh observes the create at 100/100, so the later delete must survive publication
	// as -4 for this SKU to become available.
	usageAPI.Usages.Reset()
	usageAPI.Usages.Append(newUsage(familyName, 100))
	fetchStarted := make(chan struct{})
	continueFetch := make(chan struct{})
	usageAPI.BeforeFetch = func() {
		close(fetchStarted)
		<-continueFetch
	}
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- quotaProvider.Update(ctx)
	}()

	<-fetchStarted
	g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeTrue())
	close(continueFetch)
	g.Expect(<-updateDone).To(Succeed())

	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue(), "the post-watermark delete must replace the pre-refresh create adjustment")
	g.Expect(quotaProvider.RecordDelete(nodeClaim, familyName, vCPUs)).To(BeFalse())
}

func Test_RecordCreateSkipsRefreshInProgress(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, _, quotaProvider := newTestProvider(t)
	sku := fake.MakeSKU("Standard_D4s_v3")
	familyName := sku.GetFamilyName()
	vCPUs := lo.Must(sku.VCPU())

	usageAPI.Usages.Append(newUsage(familyName, 96))
	g.Expect(quotaProvider.Update(ctx)).To(Succeed())
	generation := quotaProvider.SnapshotGeneration()

	fetchStarted := make(chan struct{})
	continueFetch := make(chan struct{})
	usageAPI.BeforeFetch = func() {
		close(fetchStarted)
		<-continueFetch
	}
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- quotaProvider.Update(ctx)
	}()

	<-fetchStarted
	g.Expect(quotaProvider.RecordCreate(generation, testNodeClaim("create-during-refresh"), familyName, vCPUs)).To(BeFalse())
	close(continueFetch)
	g.Expect(<-updateDone).To(Succeed())
	g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(BeTrue(), "the ambiguous create must not be double-counted")
}

func newTestProvider(t *testing.T) (*fake.UsageAPI, *fake.QuotaCategoryVMFamilyMappingAPI, *quota.DefaultProvider) {
	t.Helper()
	return newTestProviderWithClock(t, clock.RealClock{})
}

func newTestProviderWithClock(t *testing.T, clk clock.PassiveClock) (*fake.UsageAPI, *fake.QuotaCategoryVMFamilyMappingAPI, *quota.DefaultProvider) {
	t.Helper()
	usageAPI := &fake.UsageAPI{}
	quotaCategoryVMFamilyMappingAPI := &fake.QuotaCategoryVMFamilyMappingAPI{}
	return usageAPI, quotaCategoryVMFamilyMappingAPI, quota.NewProvider(usageAPI, quotaCategoryVMFamilyMappingAPI, fake.Region, clk)
}

func testNodeClaim(name string) *karpv1.NodeClaim {
	return &karpv1.NodeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")}}
}

func newUsage(name string, current int32) *armcompute.Usage {
	return &armcompute.Usage{
		Name:         &armcompute.UsageName{Value: lo.ToPtr(name)},
		CurrentValue: lo.ToPtr(current),
		Limit:        lo.ToPtr[int64](100),
	}
}

func newVMFamily(name string) *armcomputelimit.VMFamily {
	return &armcomputelimit.VMFamily{
		Name: lo.ToPtr(name),
		Properties: &armcomputelimit.VMFamilyProperties{
			Category:          lo.ToPtr(quota.GeneralPurposeCategory),
			ProvisioningState: lo.ToPtr(armcomputelimit.ResourceProvisioningStateSucceeded),
		},
	}
}
