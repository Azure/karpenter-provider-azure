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
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computelimit/armcomputelimit"
	"github.com/Azure/skewer"
	"github.com/samber/lo"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
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

const (
	// GeneralPurposeCategory is the quota category for general purpose VM families when "quota categories" is enabled.
	GeneralPurposeCategory = "generalPurposeCategory"

	DeleteTombstoneTTL             = 25 * time.Minute
	DeleteTombstoneCleanupInterval = 10 * time.Minute
	MaxDeleteTombstones            = 50000
	UpdateTimeout                  = 10 * time.Second
)

type adjustment struct {
	quotaName string
	vCPUs     int64
}

type deleteTombstone struct {
	lastSeen time.Time
}

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
	// HasQuotaForSKUs evaluates all SKUs against one coherent effective quota view.
	HasQuotaForSKUs(ctx context.Context, skus []*skewer.SKU) (map[string]bool, uint64)
	// SeqNum returns a monotonically increasing identifier for effective quota state. It changes
	// whenever a refresh, create, delete, or reset can alter quota-dependent SKU availability.
	SeqNum() uint64
	// SnapshotGeneration identifies the latest successfully published Azure quota snapshot.
	SnapshotGeneration() uint64
	// RecordCreate records quota consumed by an accepted create if expectedGeneration is current.
	RecordCreate(expectedGeneration uint64, nodeClaim *karpv1.NodeClaim, familyName string, vCPUs int64) bool
	// RecordDelete records quota released by an instance that the provider reports as not found.
	RecordDelete(nodeClaim *karpv1.NodeClaim, familyName string, vCPUs int64) bool
	// Reset clears cached quota data and active adjustments, causing HasQuotaFor to fail open for all SKUs.
	Reset()
}

var _ Provider = &DefaultProvider{}

type DefaultProvider struct {
	usageClient                        UsageAPI
	quotaCategoryVMFamilyMappingClient QuotaCategoryVMFamilyMappingAPI
	location                           string
	clock                              clock.PassiveClock
	// mu protects quota snapshots and lifecycle state. Update holds it across its bounded Azure
	// calls so publication is atomic with respect to creates, deletes, and quota reads.
	mu          sync.RWMutex
	snapshot    snapshot
	initialized bool
	cm          *pretty.ChangeMonitor

	// In-flight quota management bridges the gap between periodic Azure usage snapshots and accepted
	// creates/deletes. It overlays one signed net adjustment per NodeClaim on the last successful
	// snapshot so deleted quota is available immediately and new allocations consume quota.
	// When lifecycle ordering is ambiguous, it prefers the lowest plausible usage; Azure
	// remains authoritative and each successful refresh clears all active adjustments.
	//
	// snapshotGeneration prevents creates issued against an older usages baseline from being recorded after
	// a refresh.
	snapshotGeneration     uint64
	seqNum                 uint64
	adjustments            map[string]adjustment
	adjustmentsByQuotaName map[string]int64
	// deleteTombstones suppress repeated not-found observations across refreshes.
	deleteTombstones           map[string]deleteTombstone
	lastDeleteTombstoneCleanup time.Time
}

type snapshot struct {
	usages map[string]*armcompute.Usage
	// familyName -> quotaName mapping for quota categories
	// (when quota categories are enabled, not every family maps to the quota with that families name,
	// some map to a quota category instead)
	familyToQuotaName map[string]string
}

func NewProvider(
	usageClient UsageAPI,
	quotaCategoryVMFamilyMappingClient QuotaCategoryVMFamilyMappingAPI,
	location string,
	clk clock.PassiveClock,
) *DefaultProvider {
	return &DefaultProvider{
		usageClient:                        usageClient,
		quotaCategoryVMFamilyMappingClient: quotaCategoryVMFamilyMappingClient,
		location:                           location,
		clock:                              clk,
		snapshot: snapshot{
			usages:            map[string]*armcompute.Usage{},
			familyToQuotaName: map[string]string{},
		},
		adjustments:            map[string]adjustment{},
		adjustmentsByQuotaName: map[string]int64{},
		deleteTombstones:       map[string]deleteTombstone{},
		cm:                     pretty.NewChangeMonitor(),
	}
}

func (p *DefaultProvider) Update(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneDeleteTombstonesLocked(p.clock.Now())

	// From testing, quota API should respond within a reasonable time frame, so we set a timeout to avoid hanging indefinitely, especially since
	// we're holding a lock and don't want to block indefinitely.
	ctx, cancel := context.WithTimeout(ctx, UpdateTimeout)
	defer cancel()

	freshUsages, err := p.listUsages(ctx)
	if err != nil {
		p.logTimeout(ctx, err)
		return err
	}
	freshFamilyToQuotaName, err := p.buildFamilyToQuotaName(ctx, freshUsages)
	if err != nil {
		p.logTimeout(ctx, err)
		return err
	}

	p.publishSnapshotLocked(ctx, freshUsages, freshFamilyToQuotaName)
	return nil
}

func (p *DefaultProvider) logTimeout(ctx context.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		timeoutErr := err
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			timeoutErr = ctx.Err()
		}
		log.FromContext(ctx).Error(timeoutErr, "quota refresh timed out", "timeout", UpdateTimeout)
	}
}

func (p *DefaultProvider) buildFamilyToQuotaName(ctx context.Context, usages map[string]*armcompute.Usage) (map[string]string, error) {
	familyToQuotaName := map[string]string{}
	if _, enabled := usages[GeneralPurposeCategory]; !enabled {
		return familyToQuotaName, nil
	}
	familyToCategory, err := p.listQuotaCategoryToFamilyMappings(ctx)
	if err != nil {
		return nil, err
	}
	for familyName, categoryName := range familyToCategory {
		if _, ok := usages[categoryName]; ok {
			familyToQuotaName[familyName] = categoryName
		}
	}
	return familyToQuotaName, nil
}

func (p *DefaultProvider) publishSnapshotLocked(ctx context.Context, freshUsages map[string]*armcompute.Usage, freshFamilyToQuotaName map[string]string) {
	p.snapshot = snapshot{
		usages:            freshUsages,
		familyToQuotaName: freshFamilyToQuotaName,
	}
	p.initialized = true
	p.snapshotGeneration++
	p.seqNum++
	p.adjustments = map[string]adjustment{}
	p.adjustmentsByQuotaName = map[string]int64{}
	// Note: We don't clear delete tombstones here. They are pruned separately based on their age.
	usagesChanged := p.cm.HasChanged("quota-usages", freshUsages)
	mappingsChanged := p.cm.HasChanged("quota-family-mappings", freshFamilyToQuotaName)
	if usagesChanged {
		log.FromContext(ctx).V(1).Info("updated quota usages", "familyQuotas", formatFamilyQuotas(freshUsages))
	}
	if mappingsChanged {
		log.FromContext(ctx).V(1).Info("updated quota category VM family mappings", "mappedFamilies", len(freshFamilyToQuotaName))
	}
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
	quotaName := p.quotaNameLocked(familyName)
	usage, found := p.snapshot.usages[quotaName]
	return found, usage
}

func (p *DefaultProvider) GetTotalRegionalUsage() (bool, *armcompute.Usage) {
	return p.GetUsage("cores")
}

func (p *DefaultProvider) HasQuotaFor(ctx context.Context, sku *skewer.SKU) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hasQuotaForLocked(ctx, sku)
}

func (p *DefaultProvider) HasQuotaForSKUs(ctx context.Context, skus []*skewer.SKU) (map[string]bool, uint64) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]bool, len(skus))
	for _, sku := range skus {
		result[sku.GetName()] = p.hasQuotaForLocked(ctx, sku)
	}
	return result, p.seqNum
}

func (p *DefaultProvider) SeqNum() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.seqNum
}

func (p *DefaultProvider) hasQuotaForLocked(ctx context.Context, sku *skewer.SKU) bool {
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

	quotaName := p.quotaNameLocked(familyName)
	usage, found := p.snapshot.usages[quotaName]
	if !found {
		return true // fail open
	}
	if usage.Limit == nil || usage.CurrentValue == nil {
		log.FromContext(ctx).V(1).Info("WARNING: quota entry has nil Limit or CurrentValue; assuming quota available", "quotaName", lo.FromPtr(usage.Name.Value))
		return true // fail open
	}
	effectiveCurrentValue := max(0, int64(*usage.CurrentValue)+p.adjustmentsByQuotaName[quotaName])
	remaining := *usage.Limit - effectiveCurrentValue
	return vcpus <= remaining
}

func (p *DefaultProvider) SnapshotGeneration() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snapshotGeneration
}

func (p *DefaultProvider) RecordCreate(expectedGeneration uint64, nodeClaim *karpv1.NodeClaim, familyName string, vCPUs int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.initialized || expectedGeneration != p.snapshotGeneration || vCPUs <= 0 {
		return false
	}
	quotaName := p.quotaNameLocked(familyName)
	if !p.hasQuotaUsageLocked(quotaName) {
		return false
	}
	key := instanceKey(nodeClaim)
	// no-op duplicate adjustments
	if _, ok := p.adjustments[key]; ok {
		return false
	}
	// A NodeClaim can have a create accepted but fail in the LRO. That produces a RecordCreate -> RecordDelete -> RecordCreate sequence.
	// If we have no adjustment but we do have a deleteTombstones this is what happened. We clean the tombstone in this case.
	delete(p.deleteTombstones, key)
	p.adjustments[key] = adjustment{quotaName: quotaName, vCPUs: vCPUs}
	p.adjustmentsByQuotaName[quotaName] += vCPUs
	p.seqNum++
	return true
}

func (p *DefaultProvider) RecordDelete(nodeClaim *karpv1.NodeClaim, familyName string, vCPUs int64) bool {
	now := p.clock.Now()
	key := instanceKey(nodeClaim)

	p.mu.Lock()
	defer p.mu.Unlock()

	p.pruneDeleteTombstonesLocked(now)
	if !p.initialized || vCPUs <= 0 {
		return false
	}
	quotaName := p.quotaNameLocked(familyName)
	if !p.hasQuotaUsageLocked(quotaName) {
		return false
	}
	if tombstone, found := p.deleteTombstones[key]; found {
		if now.Sub(tombstone.lastSeen) > DeleteTombstoneTTL {
			// An expired tombstone no longer suppresses this observation. Remove it before
			// treating the current not-found as a new event and inserting a fresh tombstone.
			delete(p.deleteTombstones, key)
		} else {
			// This is a duplicate observation of the same deletion. Extend the sliding
			// expiration window without subtracting quota again.
			tombstone.lastSeen = now
			p.deleteTombstones[key] = tombstone
			return false
		}
	}

	if existing, ok := p.adjustments[key]; ok {
		p.insertDeleteTombstoneLocked(key, now)
		// If there's already an existing deletion adjustment for this instance, we should not subtract quota again.
		if existing.vCPUs < 0 {
			return false
		}
		p.adjustmentsByQuotaName[existing.quotaName] -= existing.vCPUs
		delete(p.adjustments, key)
		p.seqNum++
		return true
	}
	// No existing adjustment for this instance, so we should insert a new deletion adjustment and subtract its vCPUs from the quota.
	p.insertDeleteTombstoneLocked(key, now)
	p.adjustments[key] = adjustment{quotaName: quotaName, vCPUs: -vCPUs}
	p.adjustmentsByQuotaName[quotaName] -= vCPUs
	p.seqNum++
	return true
}

func instanceKey(nodeClaim *karpv1.NodeClaim) string {
	return fmt.Sprintf("%s/%s", nodeClaim.Name, nodeClaim.UID)
}

func (p *DefaultProvider) quotaNameLocked(familyName string) string {
	if categoryName, ok := p.snapshot.familyToQuotaName[familyName]; ok {
		return categoryName
	}
	return familyName
}

func (p *DefaultProvider) hasQuotaUsageLocked(quotaName string) bool {
	usage, ok := p.snapshot.usages[quotaName]
	return ok && usage != nil && usage.Limit != nil && usage.CurrentValue != nil
}

func (p *DefaultProvider) pruneDeleteTombstonesLocked(now time.Time) {
	if !p.lastDeleteTombstoneCleanup.IsZero() && now.Sub(p.lastDeleteTombstoneCleanup) < DeleteTombstoneCleanupInterval {
		return
	}
	for key, tombstone := range p.deleteTombstones {
		if now.Sub(tombstone.lastSeen) > DeleteTombstoneTTL {
			delete(p.deleteTombstones, key)
		}
	}
	p.lastDeleteTombstoneCleanup = now
}

func (p *DefaultProvider) insertDeleteTombstoneLocked(instanceKey string, now time.Time) {
	if len(p.deleteTombstones) >= MaxDeleteTombstones {
		oldestKey := ""
		var oldest time.Time
		for key, tombstone := range p.deleteTombstones {
			if oldestKey == "" || tombstone.lastSeen.Before(oldest) {
				oldestKey = key
				oldest = tombstone.lastSeen
			}
		}
		delete(p.deleteTombstones, oldestKey)
	}
	p.deleteTombstones[instanceKey] = deleteTombstone{lastSeen: now}
}

func (p *DefaultProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resetLocked()
}

func (p *DefaultProvider) resetLocked() {
	// pre-check to avoid unnecessarily updating snapshotGeneration
	if !p.initialized && len(p.snapshot.usages) == 0 && len(p.snapshot.familyToQuotaName) == 0 && len(p.adjustments) == 0 && len(p.deleteTombstones) == 0 {
		return
	}
	p.snapshot = snapshot{
		usages:            map[string]*armcompute.Usage{},
		familyToQuotaName: map[string]string{},
	}
	p.initialized = false
	p.snapshotGeneration++
	p.seqNum++
	p.adjustments = map[string]adjustment{}
	p.adjustmentsByQuotaName = map[string]int64{}
	p.deleteTombstones = map[string]deleteTombstone{}
	p.lastDeleteTombstoneCleanup = time.Time{}
	p.cm = pretty.NewChangeMonitor()
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
