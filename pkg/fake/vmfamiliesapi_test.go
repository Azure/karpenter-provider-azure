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

package fake

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computelimit/armcomputelimit"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/quota"
)

func TestQuotaCategoryVMFamilyMappingAPI_PaginatesConfiguredFamilies(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	api := &QuotaCategoryVMFamilyMappingAPI{PageSize: 2}
	for _, name := range []string{"standardBSFamily", "standardDSv3Family", "standardDv5Family"} {
		api.VMFamilies.Append(&armcomputelimit.VMFamily{
			Name: lo.ToPtr(name),
			Properties: &armcomputelimit.VMFamilyProperties{
				Category:          lo.ToPtr(quota.GeneralPurposeCategory),
				ProvisioningState: lo.ToPtr(armcomputelimit.ResourceProvisioningStateSucceeded),
			},
		})
	}

	var names []string
	pager := api.NewListBySubscriptionLocationResourcePager(Region, nil)
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		g.Expect(err).ToNot(HaveOccurred())
		names = append(names, lo.Map(page.Value, func(family *armcomputelimit.VMFamily, _ int) string {
			return lo.FromPtr(family.Name)
		})...)
	}

	g.Expect(names).To(Equal([]string{"standardBSFamily", "standardDSv3Family", "standardDv5Family"}))
	g.Expect(api.Calls()).To(Equal(int64(2)))
}
