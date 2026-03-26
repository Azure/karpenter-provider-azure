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
