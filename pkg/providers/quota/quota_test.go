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

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

func Test_Update_PopulatesUsageData(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, quotaProvider := newTestProvider(t)

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
}

func Test_GetTotalRegionalUsage_ReturnsCoresUsage(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, quotaProvider := newTestProvider(t)

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
	_, quotaProvider := newTestProvider(t)

	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, _ := quotaProvider.GetUsage("nonExistentFamily")
	g.Expect(found).To(BeFalse())
}

func Test_GetTotalRegionalUsage_ReturnsFalseWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	_, quotaProvider := newTestProvider(t)

	err := quotaProvider.Update(ctx)
	g.Expect(err).ToNot(HaveOccurred())

	found, _ := quotaProvider.GetTotalRegionalUsage()
	g.Expect(found).To(BeFalse())
}

func Test_Update_PreservesCachedDataOnFailure(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, quotaProvider := newTestProvider(t)

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

func newTestProvider(t *testing.T) (*fake.UsageAPI, *quota.DefaultProvider) {
	t.Helper()
	usageAPI := &fake.UsageAPI{}
	return usageAPI, quota.NewProvider(usageAPI, fake.Region)
}

func Test_HasQuotaFor(t *testing.T) {
	t.Parallel()
	sku := fake.MakeSKU("Standard_D4s_v3") // 4 vCPUs, standardDSv3Family

	tests := []struct {
		name     string
		usages   []*armcompute.Usage
		update   bool // whether to call Update before checking
		expected bool
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := TestContextWithLogger(t)
			g := NewWithT(t)
			usageAPI, quotaProvider := newTestProvider(t)

			if len(tc.usages) > 0 {
				usageAPI.Usages.Append(tc.usages...)
			}
			if tc.update {
				lo.Must0(quotaProvider.Update(ctx))
			}

			g.Expect(quotaProvider.HasQuotaFor(ctx, sku)).To(Equal(tc.expected))
		})
	}
}

func Test_SeqNum_IncrementsOnlyWhenDataChanges(t *testing.T) {
	t.Parallel()
	ctx := TestContextWithLogger(t)
	g := NewWithT(t)
	usageAPI, quotaProvider := newTestProvider(t)

	g.Expect(quotaProvider.SeqNum()).To(Equal(uint64(0)))

	// First update with empty data → changes from initial state
	lo.Must0(quotaProvider.Update(ctx))
	g.Expect(quotaProvider.SeqNum()).To(Equal(uint64(1)))

	// Second update with same empty data → no change, seqNum stays
	lo.Must0(quotaProvider.Update(ctx))
	g.Expect(quotaProvider.SeqNum()).To(Equal(uint64(1)))

	// Third update with new data → changes, seqNum increments
	usageAPI.Usages.Append(&armcompute.Usage{
		Name:         &armcompute.UsageName{Value: lo.ToPtr("standardDSv3Family")},
		CurrentValue: lo.ToPtr[int32](10),
		Limit:        lo.ToPtr[int64](100),
	})
	lo.Must0(quotaProvider.Update(ctx))
	g.Expect(quotaProvider.SeqNum()).To(Equal(uint64(2)))

	// Fourth update with same data → no change, seqNum stays
	lo.Must0(quotaProvider.Update(ctx))
	g.Expect(quotaProvider.SeqNum()).To(Equal(uint64(2)))
}
