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
	"github.com/Azure/skewer"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"
)

type UsageAPI interface {
	NewListPager(location string, options *armcompute.UsageClientListOptions) *runtime.Pager[armcompute.UsageClientListResponse]
}

// Provider exposes Azure compute quota/usage data for a given region.
type Provider interface {
	// Update fetches the latest quota usage data from the Azure Compute Usage API.
	Update(ctx context.Context) error
	// GetUsage returns the usage entry for the given VM family name (e.g. "standardBSFamily").
	// The bool indicates whether the family was found.
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
	usageClient UsageAPI
	location    string
	mu          sync.RWMutex
	usages      map[string]*armcompute.Usage
	cm          *pretty.ChangeMonitor
	seqNum      uint64
}

func NewProvider(usageClient UsageAPI, location string) *DefaultProvider {
	return &DefaultProvider{
		usageClient: usageClient,
		location:    location,
		usages:      map[string]*armcompute.Usage{},
		cm:          pretty.NewChangeMonitor(),
	}
}

func (p *DefaultProvider) Update(ctx context.Context) error {
	freshUsages := map[string]*armcompute.Usage{}

	pager := p.usageClient.NewListPager(p.location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, usage := range page.Value {
			// Note that the usages API also returns entries for non-family categories, such as
			// "cores" (total regional vCPU usage), "PremiumDiskCount", etc.
			// We currently include these in our map as it doesn't harm anything, although they are not used currently.
			if usage != nil && usage.Name != nil && usage.Name.Value != nil {
				freshUsages[*usage.Name.Value] = usage
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.usages = freshUsages
	if p.cm.HasChanged("quota-usages", freshUsages) {
		atomic.AddUint64(&p.seqNum, 1)
		log.FromContext(ctx).V(1).Info("updated quota usages", "familyQuotas", formatFamilyQuotas(freshUsages))
	}
	return nil
}

func (p *DefaultProvider) GetUsage(familyName string) (bool, *armcompute.Usage) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	usage, ok := p.usages[familyName]
	return ok, usage
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
	found, usage := p.GetUsage(familyName)
	if !found {
		return true // fail open
	}
	if usage.Limit == nil || usage.CurrentValue == nil {
		log.FromContext(ctx).V(1).Info("WARNING: quota entry has nil Limit or CurrentValue; assuming quota available", "family", familyName)
		return true // fail open
	}
	remaining := *usage.Limit - int64(*usage.CurrentValue)
	return vcpus <= remaining
}

func (p *DefaultProvider) SeqNum() uint64 {
	return atomic.LoadUint64(&p.seqNum)
}

func (p *DefaultProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usages = map[string]*armcompute.Usage{}
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
