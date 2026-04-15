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

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/alecthomas/kong"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

var args struct {
	Format         string `kong:"required,enum='testfakes,nameonly',help='Output format: testfakes (full SKU details) or nameonly (SKU names only).'"`
	Path           string `kong:"required,help='Output file path.'"`
	Location       string `kong:"optional,help='Azure region/location (required for testfakes, not allowed for nameonly).'"`
	Sizes          string `kong:"optional,help='Comma-separated list of VM sizes to fetch (testfakes only; if omitted, all sizes are included).'"`
	IgnoreFamilies string `kong:"optional,name='ignore-families',help='Comma-separated family:date pairs to ignore (nameonly only). SKUs in the given family are excluded until the date. Example: standardDasv7Family:2026-06-01,standardEasv6Family:2026-07-01'"`
}

func main() {
	kong.Parse(&args,
		kong.Name("instancetype-testdata-gen"),
		kong.Description("Generate instance type test data from Azure SKUs."),
	)

	if args.Format == "nameonly" && args.Sizes != "" {
		panic("--sizes cannot be specified with nameonly format")
	}
	if args.Format == "nameonly" && args.Location != "" {
		panic("--location cannot be specified with nameonly format (all regions are queried)")
	}
	if args.Format == "testfakes" && args.Location == "" {
		panic("--location is required for testfakes format")
	}
	if args.Format == "testfakes" && args.IgnoreFamilies != "" {
		panic("--ignore-families cannot be specified with testfakes format")
	}

	ignored := parseIgnoreFamilies(args.IgnoreFamilies)

	fmt.Println("starting generation of sku data...")
	sub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if sub == "" {
		panic("AZURE_SUBSCRIPTION_ID env var is required")
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create credential: %v", err))
	}

	client, err := armcompute.NewResourceSKUsClient(sub, cred, nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create client: %v", err))
	}

	ctx := context.Background()

	switch args.Format {
	case "testfakes":
		pager := client.NewListPager(&armcompute.ResourceSKUsClientListOptions{
			Filter: lo.ToPtr(fmt.Sprintf("location eq '%s'", args.Location)),
		})
		var targetSkus map[string]struct{}
		if args.Sizes != "" {
			targetSkus = map[string]struct{}{}
			for _, s := range strings.Split(args.Sizes, ",") {
				targetSkus[s] = struct{}{}
			}
		}
		skuData := []*armcompute.ResourceSKU{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				panic(fmt.Sprintf("failed to get next page: %v", err))
			}
			for _, sku := range page.Value {
				if targetSkus != nil {
					if _, ok := targetSkus[*sku.Name]; !ok {
						continue
					}
				}
				skuData = append(skuData, sku)
			}
		}
		fmt.Println("Successfully Fetched all the SKUs", len(skuData))
		writeTestFakes(skuData, args.Location, args.Path)

	case "nameonly":
		pager := client.NewListPager(&armcompute.ResourceSKUsClientListOptions{})
		skuMap := map[string]*armcompute.ResourceSKU{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				panic(fmt.Sprintf("failed to get next page: %v", err))
			}
			for _, sku := range page.Value {
				if sku.ResourceType != nil && *sku.ResourceType == "virtualMachines" && sku.Name != nil {
					name := *sku.Name
					if _, exists := skuMap[name]; !exists {
						skuMap[name] = sku
					}
				}
			}
		}
		// Sort by name
		names := lo.Keys(skuMap)
		sort.Strings(names)

		sortedSKUs := lo.Map(names, func(name string, _ int) *armcompute.ResourceSKU {
			return skuMap[name]
		})
		fmt.Println("Successfully Fetched VM SKU names:", len(sortedSKUs))
		writeNameOnly(sortedSKUs, args.Path, ignored)
	}
	fmt.Println("Successfully Generated output at", args.Path)
}

func readExistingSKUs(path string) map[string]instancetype.SKUEntry {
	existing := map[string]instancetype.SKUEntry{}
	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist yet; that's fine
		return existing
	}
	var entries []instancetype.SKUEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		panic(fmt.Sprintf("failed to parse existing YAML at %s: %v", path, err))
	}
	for _, e := range entries {
		existing[e.Name] = e
	}
	return existing
}

// parseIgnoreFamilies parses a comma-separated string of "family:date" pairs.
// SKUs in the given family are excluded until the specified date (inclusive).
func parseIgnoreFamilies(raw string) map[string]time.Time {
	result := map[string]time.Time{}
	if raw == "" {
		return result
	}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			panic(fmt.Sprintf("invalid --ignore-families entry %q: expected family:date", pair))
		}
		until, err := time.Parse("2006-01-02", parts[1])
		if err != nil {
			panic(fmt.Sprintf("invalid date in --ignore-families entry %q: %v", pair, err))
		}
		result[parts[0]] = until
	}
	return result
}

func writeNameOnly(skus []*armcompute.ResourceSKU, path string, ignoredFamilies map[string]time.Time) {
	existing := readExistingSKUs(path)
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	entries := make([]instancetype.SKUEntry, 0, len(skus))
	for _, sku := range skus {
		name := lo.FromPtrOr(sku.Name, "")
		family := lo.FromPtrOr(sku.Family, "")
		if until, ok := ignoredFamilies[family]; ok && !now.After(until) {
			fmt.Printf("ignoring %s (family %s ignored until %s)\n", name, family, until.Format("2006-01-02"))
			continue
		}
		discoveredOn := today
		if prev, ok := existing[name]; ok && prev.DiscoveredOn != "" {
			discoveredOn = prev.DiscoveredOn
		}
		entries = append(entries, instancetype.SKUEntry{
			Name:         name,
			Family:       family,
			DiscoveredOn: discoveredOn,
		})
	}

	out, err := yaml.Marshal(entries)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal YAML: %v", err))
	}

	header := []byte("# This file is auto-generated. DO NOT EDIT.\n")
	fmt.Println("writing file to", path)
	if err := os.WriteFile(path, append(header, out...), 0644); err != nil {
		panic(fmt.Sprintf("failed to write file: %v", err))
	}
}

func writeTestFakes(ResourceSkus []*armcompute.ResourceSKU, location, path string) {
	src := &bytes.Buffer{}
	fmt.Fprintln(src, "//go:build !ignore_autogenerated")
	license := lo.Must(os.ReadFile("hack/boilerplate.go.txt"))
	fmt.Fprintln(src, string(license))
	fmt.Fprintln(src, "package fake")
	fmt.Fprintln(src, "import (")
	fmt.Fprintln(src, `	"github.com/samber/lo"`)
	fmt.Fprintln(src, `	//nolint:staticcheck // deprecated package`)
	fmt.Fprintln(src, `	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2022-08-01/compute"`)
	fmt.Fprintln(src, ")")
	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(src, "// generated at %s\n\n\n", now)
	fmt.Fprintln(src, "func init() {")
	fmt.Fprintln(src, "// ResourceSkus is a list of selected VM SKUs for a given region")
	fmt.Fprintf(src, "ResourceSkus[%q] = []compute.ResourceSku{\n", location)
	for _, sku := range ResourceSkus {
		fmt.Fprintln(src, "	{")
		fmt.Fprintf(src, "		Name:         lo.ToPtr(%q),\n", lo.FromPtrOr(sku.Name, ""))
		fmt.Fprintf(src, "		Tier:         lo.ToPtr(%q),\n", lo.FromPtrOr(sku.Tier, ""))
		fmt.Fprintf(src, "		Kind:         lo.ToPtr(%q),\n", lo.FromPtrOr(sku.Kind, ""))
		fmt.Fprintf(src, "		Size:         lo.ToPtr(%q),\n", lo.FromPtrOr(sku.Size, ""))
		fmt.Fprintf(src, "		Family:       lo.ToPtr(%q),\n", lo.FromPtrOr(sku.Family, ""))
		fmt.Fprintf(src, "		ResourceType: lo.ToPtr(%q),\n", lo.FromPtrOr(sku.ResourceType, ""))
		fmt.Fprintln(src, "		APIVersions: &[]string{")
		for _, apiVersion := range sku.APIVersions {
			fmt.Fprintf(src, "			lo.ToPtr(%q),\n", lo.FromPtrOr(apiVersion, ""))
		}
		fmt.Fprintln(src, "		},")
		if sku.Capacity != nil {
			fmt.Fprintln(src, "		Capacity: compute.ResourceSkuCapacity{")
			fmt.Fprintf(src, "			Minimum: lo.ToPtr(%d),\n", lo.FromPtrOr(sku.Capacity.Minimum, 0))
			fmt.Fprintf(src, "			Maximum: lo.ToPtr(%d),\n", lo.FromPtrOr(sku.Capacity.Maximum, 0))
			fmt.Fprintf(src, "			Default: lo.ToPtr(%d),\n", lo.FromPtrOr(sku.Capacity.Default, 0))
			fmt.Fprintf(src, "		},")
		}
		fmt.Fprintf(src, "		Costs: &[]compute.ResourceSkuCosts{")
		for _, cost := range sku.Costs {
			fmt.Fprintf(src, "			{MeterID: lo.ToPtr(%q), Quantity: lo.ToPtr(%q), ExtendedUnit: lo.ToPtr(%q)},", lo.FromPtrOr(cost.MeterID, ""), lo.FromPtrOr(cost.Quantity, 0.0), lo.FromPtrOr(cost.ExtendedUnit, ""))
		}
		fmt.Fprintln(src, "		},")
		fmt.Fprintln(src, "		Restrictions: &[]compute.ResourceSkuRestrictions{")
		for _, restriction := range sku.Restrictions {
			fmt.Fprintln(src, "			{")
			fmt.Fprintf(src, "				Type: compute.ResourceSkuRestrictionsType(%q),\n", lo.FromPtrOr(restriction.Type, ""))
			for _, value := range restriction.Values {
				fmt.Fprintf(src, "				Values: &[]string{%q},", lo.FromPtrOr(value, ""))
			}
			fmt.Fprintln(src)
			fmt.Fprintln(src, "				RestrictionInfo: &compute.ResourceSkuRestrictionInfo{")
			fmt.Fprintln(src, "					Locations: &[]string{")
			for _, location := range restriction.RestrictionInfo.Locations {
				fmt.Fprintf(src, "						%q,\n", lo.FromPtr(location))
			}
			fmt.Fprintln(src, "					},")
			fmt.Fprintln(src, "					Zones: &[]string{")
			for _, zone := range restriction.RestrictionInfo.Zones {
				fmt.Fprintf(src, "						%q,\n", lo.FromPtr(zone))
			}
			fmt.Fprintln(src, "					},")
			fmt.Fprintln(src, "				},")
			fmt.Fprintf(src, "				ReasonCode: %q,\n", lo.FromPtrOr(restriction.ReasonCode, ""))
			fmt.Fprintln(src, "			},")
		}
		fmt.Fprintln(src, "		},")
		fmt.Fprintln(src, "		Capabilities: &[]compute.ResourceSkuCapabilities{")
		for _, capability := range sku.Capabilities {
			fmt.Fprintf(src, "			{Name: lo.ToPtr(%q), Value: lo.ToPtr(%q)},\n", *capability.Name, *capability.Value)
		}
		fmt.Fprintln(src, "		},")
		fmt.Fprintf(src, "		Locations: &[]string{%q},\n", location)
		fmt.Fprintf(src, "		LocationInfo: &[]compute.ResourceSkuLocationInfo{")
		for _, locationInfo := range sku.LocationInfo {
			fmt.Fprintf(src, "			{Location: lo.ToPtr(%q),", location)
			fmt.Fprintln(src, "			Zones: &[]string{")
			sort.Slice(locationInfo.Zones, func(i, j int) bool {
				return *locationInfo.Zones[i] < *locationInfo.Zones[j]
			})
			for _, zone := range locationInfo.Zones {
				fmt.Fprintf(src, "				%q,\n", lo.FromPtr(zone))
			}
			fmt.Fprintln(src, "			},")
		}
		fmt.Fprintln(src, "			},")
		fmt.Fprintln(src, "	},")

		fmt.Fprintln(src, "},")
	}

	fmt.Fprintln(src, "}")
	fmt.Fprintln(src, "}")
	fmt.Println("writing file to", path)
	if err := os.WriteFile(path, src.Bytes(), 0600); err != nil {
		panic(fmt.Sprintf("failed to write file: %v", err))
	}
}
