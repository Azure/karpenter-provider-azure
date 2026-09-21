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

package quota

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computelimit/armcomputelimit"
	"github.com/Azure/skewer"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

type UsageAPI interface {
	NewListPager(location string, options *armcompute.UsageClientListOptions) *runtime.Pager[armcompute.UsageClientListResponse]
}

type QuotaCategoryVMFamilyMappingAPI interface {
	NewListBySubscriptionLocationResourcePager(
		location string,
		options *armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceOptions,
	) *runtime.Pager[armcomputelimit.VMFamiliesClientListBySubscriptionLocationResourceResponse]
}

// GeneralPurposeCategory is the quota category for general purpose VM families when "quota categories" is enabled.
const GeneralPurposeCategory = "generalPurposeCategory"

// Provider exposes Azure compute quota/usage data for a given region.
type Provider interface {
	// Update fetches the latest quota usage data from the Azure Compute Usage API.
	Update(ctx context.Context) error
	// GetUsage returns the effective usage entry for the given VM family name (e.g. "standardBSFamily").
	// If the family belongs to a quota category, it returns that category's usage entry.
	// The bool indicates whether the effective usage entry was found.
	GetUsage(familyName string) (bool, *armcompute.Usage)
	// GetTotalRegionalUsage returns the total regional vCPU usage (the "cores" entry).
	// The bool indicates whether the entry was found.
	GetTotalRegionalUsage() (bool, *armcompute.Usage)
	// HasQuotaFor returns true if the SKU's family has enough remaining quota
	// to accommodate the SKU's vCPU count. Returns true (fail-open) if quota
	// data is unavailable, the family is not found in the cached data, or
	// the SKU's family name or vCPU count cannot be determined.
	HasQuotaFor(ctx context.Context, sku *skewer.SKU) bool
	// SeqNum returns a monotonically increasing counter that is incremented
	// each time quota data is successfully refreshed. Consumers can use this
	// to invalidate caches that depend on quota state.
	SeqNum() uint64
	// Reset clears all cached quota data, causing HasQuotaFor to fail open for all SKUs.
	Reset()
}

var _ Provider = &DefaultProvider{}

type DefaultProvider struct {
	usageClient                        UsageAPI
	quotaCategoryVMFamilyMappingClient QuotaCategoryVMFamilyMappingAPI
	location                           string
	mu                                 sync.RWMutex
	snapshot                           snapshot
	cm                                 *pretty.ChangeMonitor
	seqNum                             atomic.Uint64
}

type snapshot struct {
	usages            map[string]*armcompute.Usage
	familyToQuotaName map[string]string
}

func NewProvider(usageClient UsageAPI, quotaCategoryVMFamilyMappingClient QuotaCategoryVMFamilyMappingAPI, location string) *DefaultProvider {
	return &DefaultProvider{
		usageClient:                        usageClient,
		quotaCategoryVMFamilyMappingClient: quotaCategoryVMFamilyMappingClient,
		location:                           location,
		snapshot: snapshot{
			usages:            map[string]*armcompute.Usage{},
			familyToQuotaName: map[string]string{},
		},
		cm: pretty.NewChangeMonitor(),
	}
}

func (p *DefaultProvider) Update(ctx context.Context) error {
	freshUsages, err := p.listUsages(ctx)
	if err != nil {
		return err
	}
	// Build mapping of family to quota (for quota categories)
	freshFamilyToQuotaName := map[string]string{}
	if _, enabled := freshUsages[GeneralPurposeCategory]; enabled {
		familyToCategory, err := p.listQuotaCategoryToFamilyMappings(ctx)
		if err != nil {
			return err
		}
		for familyName, categoryName := range familyToCategory {
			if _, ok := freshUsages[categoryName]; ok {
				freshFamilyToQuotaName[familyName] = categoryName
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot = snapshot{
		usages:            freshUsages,
		familyToQuotaName: freshFamilyToQuotaName,
	}
	usagesChanged := p.cm.HasChanged("quota-usages", freshUsages)
	mappingsChanged := p.cm.HasChanged("quota-family-mappings", freshFamilyToQuotaName)
	if usagesChanged || mappingsChanged {
		p.seqNum.Add(1)
	}
	if usagesChanged {
		log.FromContext(ctx).V(1).Info("updated quota usages", "familyQuotas", formatFamilyQuotas(freshUsages))
	}
	if mappingsChanged {
		log.FromContext(ctx).V(1).Info("updated quota category VM family mappings", "mappedFamilies", len(freshFamilyToQuotaName))
	}
	return nil
}

func (p *DefaultProvider) listUsages(ctx context.Context) (map[string]*armcompute.Usage, error) {
	usages := map[string]*armcompute.Usage{}
	pager := p.usageClient.NewListPager(p.location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, usage := range page.Value {
			// Note that the usages API also returns entries for non-family categories, such as
			// "cores" (total regional vCPU usage), "PremiumDiskCount", etc.
			// We currently include these in our map as it doesn't harm anything, although they are not used currently.
			if usage != nil && usage.Name != nil && usage.Name.Value != nil {
				usages[*usage.Name.Value] = usage
			}
		}
	}
	return usages, nil
}

func (p *DefaultProvider) listQuotaCategoryToFamilyMappings(ctx context.Context) (map[string]string, error) {
	familyToCategory := map[string]string{}
	pager := p.quotaCategoryVMFamilyMappingClient.NewListBySubscriptionLocationResourcePager(p.location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing Compute Limit VM families: %w", err)
		}
		for _, family := range page.Value {
			familyName, categoryName := resolveFamilyQuotaName(family)
			if familyName == "" || categoryName == "" {
				continue
			}
			familyToCategory[familyName] = categoryName
		}
	}
	return familyToCategory, nil
}

func resolveFamilyQuotaName(family *armcomputelimit.VMFamily) (string, string) {
	if family == nil || family.Properties == nil {
		return "", ""
	}
	familyName := lo.FromPtr(family.Name)
	categoryName := lo.FromPtr(family.Properties.Category)
	if familyName == "" || categoryName == "" || lo.FromPtr(family.Properties.ProvisioningState) != armcomputelimit.ResourceProvisioningStateSucceeded {
		return "", ""
	}
	return familyName, categoryName
}

func (p *DefaultProvider) GetUsage(familyName string) (bool, *armcompute.Usage) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	quotaName := familyName
	// handle quota categories
	if categoryName, ok := p.snapshot.familyToQuotaName[familyName]; ok {
		quotaName = categoryName
	}
	usage, found := p.snapshot.usages[quotaName]
	return found, usage
}

func (p *DefaultProvider) GetTotalRegionalUsage() (bool, *armcompute.Usage) {
	return p.GetUsage("cores")
}

func (p *DefaultProvider) HasQuotaFor(ctx context.Context, sku *skewer.SKU) bool {
	familyName := sku.GetFamilyName()
	if familyName == "" {
		log.FromContext(ctx).V(1).Info("WARNING: cannot check quota for SKU, family name is missing; assuming quota available", "sku", sku.GetName())
		return true // fail open
	}
	vcpus, err := sku.VCPU()
	if err != nil {
		log.FromContext(ctx).V(1).Info("WARNING: cannot check quota for SKU, vCPU count unavailable; assuming quota available", "sku", sku.GetName(), "error", err)
		return true // fail open
	}
	// Note that this may return a quota category usage object if appropriate
	found, usage := p.GetUsage(familyName)
	if !found {
		return true // fail open
	}
	if usage.Limit == nil || usage.CurrentValue == nil {
		log.FromContext(ctx).V(1).Info("WARNING: quota entry has nil Limit or CurrentValue; assuming quota available", "quotaName", lo.FromPtr(usage.Name.Value))
		return true // fail open
	}
	remaining := *usage.Limit - int64(*usage.CurrentValue)
	return vcpus <= remaining
}

func (p *DefaultProvider) SeqNum() uint64 {
	return p.seqNum.Load()
}

func (p *DefaultProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.snapshot.usages) == 0 && len(p.snapshot.familyToQuotaName) == 0 {
		return
	}
	p.snapshot = snapshot{
		usages:            map[string]*armcompute.Usage{},
		familyToQuotaName: map[string]string{},
	}
	p.cm = pretty.NewChangeMonitor()
	p.seqNum.Add(1)
}

// formatFamilyQuotas returns a compact summary of family quotas like "standardDSv3Family: 78/500, standardDv5Family: 20/100".
// Only entries containing "Family" or "family" in the name are included.
func formatFamilyQuotas(usages map[string]*armcompute.Usage) string {
	var parts []string
	for name, usage := range usages {
		if !strings.Contains(strings.ToLower(name), "family") {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %d/%d", name, lo.FromPtr(usage.CurrentValue), lo.FromPtr(usage.Limit)))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
