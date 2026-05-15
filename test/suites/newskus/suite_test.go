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

package newskus_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/sku"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	"github.com/Azure/karpenter-provider-azure/test/pkg/newskus"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	resultPass    = "PASS"
	resultFail    = "FAIL"
	resultSkipped = "SKIPPED"
	resultNotRun  = "NOT RUN"
)

var env *azure.Environment
var nodeClass *v1beta1.AKSNodeClass
var nodePool *karpv1.NodePool

// FamilyTestCase holds the test configuration and result for one canonical family.
type FamilyTestCase struct {
	// CanonicalFamily is the normalized family key, e.g. "Da_v7".
	CanonicalFamily string
	// RepresentativeSize is the VM size name chosen for testing, e.g. "Standard_D4as_v7".
	RepresentativeSize string
	// Family is the Azure quota family name from YAML, e.g. "StandardDasv7Family".
	Family string
	// AllSizes is the list of all VM sizes in this canonical family.
	AllSizes []string
	// SiblingVariants are other variant families in the same canonical group (not directly tested).
	SiblingVariants []VariantFamily
	// Reason provides context for the result (e.g. skip reason, failure details).
	Reason string
	// Result is the test outcome: "PASS", "FAIL", or "" (not run).
	Result string
}

// VariantFamily represents a variant family that shares a canonical family with the tested representative.
type VariantFamily struct {
	// Family is the Azure quota family name, e.g. "StandardDadsv7Family".
	Family string
	// ExampleSize is one example VM size from this variant, e.g. "Standard_D2ads_v7".
	ExampleSize string
}

// familyTestCases is populated at tree construction time (init) so that
// Ginkgo's Describe/It blocks can iterate over it before BeforeSuite runs.
var familyTestCases = buildTestCasesStatic()

func TestNewSKUs(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
		// Now that we have env, check for unsupported types and quota, updating Reason on each case
		quotaMap := fetchQuotaMap(env)
		for _, tc := range familyTestCases {
			checkUnsupported(tc)
			checkQuota(tc, quotaMap)
		}
		GinkgoWriter.Printf("Discovered %d canonical families to test\n", len(familyTestCases))
	})
	AfterSuite(func() {
		printSummaryReport(GinkgoWriter.Printf, familyTestCases)
		env.Stop()
	})
	RunSpecs(t, "NewSKUs")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

// buildTestCasesStatic loads SKU entries, identifies canonical families that
// are genuinely new (not just new sizes added to an already-tested family),
// and builds test cases. Does NOT check quota (no env needed),
// so it can run at init time for Ginkgo tree construction.
func buildTestCasesStatic() []*FamilyTestCase {
	entries := instancetype.GetAllSKUEntries()
	cutoff := time.Now().AddDate(0, -2, 0)

	newFamilies := filterNewFamilies(entries, cutoff)
	if len(newFamilies) == 0 {
		fmt.Fprintln(os.Stderr, "WARNING: No genuinely new canonical families found within 2-month window")
		return nil
	}

	// Flatten for buildFamilyTestCases
	filtered := lo.Flatten(lo.Values(newFamilies))
	fmt.Fprintf(os.Stderr, "Found %d genuinely new canonical families (%d total SKUs)\n", len(newFamilies), len(filtered))

	return buildFamilyTestCases(filtered)
}

// filterNewFamilies identifies canonical families that have genuinely new entries
// (discovered after cutoff) and weren't already testable before. Returns a map
// from canonical family name to ALL SKU entries for that family.
//
//nolint:gocyclo
func filterNewFamilies(entries []instancetype.SKUEntry, cutoff time.Time) map[string][]instancetype.SKUEntry {
	// Group ALL entries by canonical family
	allGroups := map[string][]instancetype.SKUEntry{}
	for _, e := range entries {
		key := newskus.CanonicalFamily(e)
		allGroups[key] = append(allGroups[key], e)
	}

	// Identify families that have at least one recently discovered entry
	familiesWithNewEntries := map[string]struct{}{}
	for _, e := range entries {
		t, err := time.Parse("2006-01-02", e.DiscoveredOn)
		if err != nil {
			continue
		}
		if !t.Before(cutoff) {
			familiesWithNewEntries[newskus.CanonicalFamily(e)] = struct{}{}
		}
	}

	// Filter out families that were already tested before: if ANY old entry
	// (before cutoff) has >=4 vCPUs and isn't constrained-CPU, the family
	// already had a suitable representative and doesn't need retesting.
	newFamilies := map[string][]instancetype.SKUEntry{}
	for family := range familiesWithNewEntries {
		skus := allGroups[family]
		alreadyTested := false
		for _, e := range skus {
			t, err := time.Parse("2006-01-02", e.DiscoveredOn)
			if err != nil || !t.Before(cutoff) {
				continue // skip recent or unparsable entries
			}
			if !newskus.IsConstrainedCPU(e.Name) && newskus.VcpuCount(e.Name) >= 4 {
				alreadyTested = true
				break
			}
		}
		if !alreadyTested {
			// Include ALL sizes for this family (old and new) so we pick the best representative
			newFamilies[family] = skus
		}
	}

	return newFamilies
}

// buildFamilyTestCases takes SKU entries, groups them by canonical family,
// picks a representative size, and collects sibling variant families.
func buildFamilyTestCases(entries []instancetype.SKUEntry) []*FamilyTestCase {
	// Group by canonical family
	groups := map[string][]instancetype.SKUEntry{}
	for _, e := range entries {
		key := newskus.CanonicalFamily(e)
		groups[key] = append(groups[key], e)
	}

	// Sort canonical family keys for deterministic ordering
	keys := lo.Keys(groups)
	sort.Strings(keys)

	var cases []*FamilyTestCase
	for _, key := range keys {
		skus := groups[key]
		rep := newskus.PickRepresentativeSize(skus)

		// Collect all sizes and sibling variant families
		allSizes := lo.Map(skus, func(s instancetype.SKUEntry, _ int) string { return s.Name })
		sort.Strings(allSizes)

		// Group siblings by their Azure family name (excluding the representative's family)
		siblingMap := map[string]string{} // family -> example size
		for _, s := range skus {
			if s.Family != rep.Family {
				if _, exists := siblingMap[s.Family]; !exists {
					siblingMap[s.Family] = s.Name
				}
			}
		}
		siblingKeys := lo.Keys(siblingMap)
		sort.Strings(siblingKeys)
		siblings := lo.Map(siblingKeys, func(f string, _ int) VariantFamily {
			return VariantFamily{Family: f, ExampleSize: siblingMap[f]}
		})

		cases = append(cases, &FamilyTestCase{
			CanonicalFamily:    key,
			RepresentativeSize: rep.Name,
			Family:             rep.Family,
			AllSizes:           allSizes,
			SiblingVariants:    siblings,
		})
	}
	return cases
}

// fetchQuotaMap fetches the current Azure compute usage/quota and returns
// a map from family name to available vCPUs.
func fetchQuotaMap(env *azure.Environment) map[string]int64 {
	quotaMap := map[string]int64{}
	usageClient, err := armcompute.NewUsageClient(env.SubscriptionID, env.GetDefaultCredential(), nil)
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to create usage client: %v\n", err)
		return quotaMap
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := usageClient.NewListPager(env.Region, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			GinkgoWriter.Printf("WARNING: failed to fetch quota page: %v\n", err)
			break
		}
		for _, usage := range page.Value {
			if usage.Name == nil || usage.Name.Value == nil {
				continue
			}
			available := lo.FromPtr(usage.Limit) - int64(lo.FromPtr(usage.CurrentValue))
			quotaMap[strings.ToLower(*usage.Name.Value)] = available
		}
	}
	return quotaMap
}

// checkUnsupported checks if the representative size is a type that Karpenter filters out
// (e.g. confidential VMs). Sets Result/Reason if so.
func checkUnsupported(tc *FamilyTestCase) {
	size := strings.TrimPrefix(tc.RepresentativeSize, "Standard_")
	if sku.IsConfidential(size) {
		tc.Result = resultSkipped
		tc.Reason = "confidential VM (not supported by Karpenter)"
	}
}

// checkQuota checks if there's sufficient quota for the test case and sets Reason if not.
func checkQuota(tc *FamilyTestCase, quotaMap map[string]int64) {
	if tc.Result == resultSkipped {
		// Already skipped for some other reason, don't need to check quota
		return
	}
	available, found := quotaMap[strings.ToLower(tc.Family)]
	if !found {
		tc.Result = resultSkipped
		tc.Reason = fmt.Sprintf("quota family %q not found in region", tc.Family)
		return
	}
	// Need at least the vCPUs of the representative size
	needed := int64(newskus.VcpuCount(tc.RepresentativeSize))
	if needed == 0 {
		needed = 4
	}
	if available < needed {
		tc.Result = resultSkipped
		tc.Reason = fmt.Sprintf("insufficient quota: %d available, %d needed for %s",
			available,
			needed,
			tc.Family)
	}
}

// resultIcon returns an emoji for the test result.
func resultIcon(result string, direct bool) string {
	switch {
	case result == resultPass && direct:
		return "✅"
	case result == resultPass && !direct:
		return "🔗"
	case result == resultFail:
		return "❌"
	case result == resultSkipped:
		return "⏭️"
	default:
		return "❓"
	}
}

// printSummaryReport prints a formatted report of all test cases to the given writer.
func printSummaryReport(printf func(format string, args ...any), cases []*FamilyTestCase) {
	printf("\n=== NewSKUs Test Summary ===\n")
	printf("%-4s %-30s %-35s %-30s %-10s %s\n", "", "Canonical Family", "Variant Family", "VM Size", "Result", "Reason")
	printf("%s\n", strings.Repeat("-", 145))
	for _, tc := range cases {
		result := tc.Result
		if result == "" {
			result = resultNotRun
		}
		icon := resultIcon(result, true)
		printf("%-4s %-30s %-35s %-30s %-10s %s\n",
			icon, tc.CanonicalFamily, tc.Family, tc.RepresentativeSize, result, tc.Reason)

		if tc.Result == resultPass {
			for _, variant := range tc.SiblingVariants {
				printf("%-4s %-30s %-35s %-30s %-10s %s\n",
					"🔗", tc.CanonicalFamily, variant.Family, variant.ExampleSize, "INFERRED PASS", "")
			}
		}
	}

	directPassed := lo.CountBy(cases, func(tc *FamilyTestCase) bool { return tc.Result == resultPass })
	inferred := lo.SumBy(cases, func(tc *FamilyTestCase) int {
		if tc.Result == resultPass {
			return len(tc.SiblingVariants)
		}
		return 0
	})
	skipped := lo.CountBy(cases, func(tc *FamilyTestCase) bool { return tc.Result == resultSkipped })
	failed := lo.CountBy(cases, func(tc *FamilyTestCase) bool { return tc.Result == resultFail })

	printf("\nDirect: %d tested | %d passed | %d failed | %d skipped\n",
		len(cases),
		directPassed,
		failed,
		skipped)
	printf("Inferred: %d variant families passed by similarity\n", inferred)

	if failed > 0 {
		printf("\nFailed families:\n")
		for _, tc := range cases {
			if tc.Result == resultFail {
				printf("  %s %s (%s)\n", resultIcon(tc.Result, true), tc.CanonicalFamily, tc.RepresentativeSize)
			}
		}
	}
}
