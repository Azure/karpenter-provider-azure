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

package cache

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Azure/skewer"
	"github.com/patrickmn/go-cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	// wholeVMFamilyBlockedSentinel means that entire SKU family is blocked, not just certain instance types with a CPU count above a threshold
	wholeVMFamilyBlockedSentinel = -1
)

var (
	spotKey = singleInstanceKey("", "", karpv1.CapacityTypeSpot)
)

// UnavailableOfferings stores any offerings that return ICE (insufficient capacity errors) when
// attempting to launch the capacity. These offerings are ignored as long as they are in the cache on
// GetInstanceTypes responses
type UnavailableOfferings struct {
	// key: <capacityType>:<instanceType>:<zone>, value: struct{}{}
	singleOfferingCache *cache.Cache
	// key: <skuFamilyName>:<zone>:<capacityType>, value: int64 (CPU count at or above which we block, or wholeVMFamilyBlockedSentinel if entire family is blocked)
	vmFamilyCache *cache.Cache
	SeqNum        uint64
}

func NewUnavailableOfferingsWithCache(singleOfferingCache, vmFamilyCache *cache.Cache) *UnavailableOfferings {
	uo := &UnavailableOfferings{
		singleOfferingCache: singleOfferingCache,
		vmFamilyCache:       vmFamilyCache,
		SeqNum:              0,
	}
	uo.singleOfferingCache.OnEvicted(func(_ string, _ interface{}) {
		atomic.AddUint64(&uo.SeqNum, 1)
	})
	uo.vmFamilyCache.OnEvicted(func(_ string, _ interface{}) {
		atomic.AddUint64(&uo.SeqNum, 1)
	})
	return uo
}

func NewUnavailableOfferings() *UnavailableOfferings {
	return NewUnavailableOfferingsWithCache(
		cache.New(UnavailableOfferingsTTL, UnavailableOfferingsCleanupInterval),
		cache.New(UnavailableOfferingsTTL, UnavailableOfferingsCleanupInterval),
	)
}

// IsUnavailable returns true if the offering appears in the cache
func (u *UnavailableOfferings) IsUnavailable(sku *skewer.SKU, zone, capacityType string) bool {
	if capacityType == karpv1.CapacityTypeSpot {
		if _, found := u.singleOfferingCache.Get(spotKey); found {
			return true
		}
	}

	// check if there offering is marked as unavailable at vm family level
	if u.isFamilyUnavailable(sku, zone, capacityType) {
		return true
	}

	// lastly check if the offering is marked as unavailable for the specific instance type, zone and capacity type
	_, found := u.singleOfferingCache.Get(singleInstanceKey(sku.GetName(), zone, capacityType))
	return found
}

func (u *UnavailableOfferings) isFamilyUnavailable(sku *skewer.SKU, zone, capacityType string) bool {
	skuVCPUCount, err := sku.VCPU()
	if err != nil {
		skuVCPUCount = 0 // default to 0 if we can't determine VCPU count
	}
	// TODO - refactor to reduce code duplication between here and instancetype.go
	skuVMSize, err := sku.GetVMSize()
	if err != nil {
		log.FromContext(context.TODO()).Error(err, "failed to get VM size for SKU", "sku", sku.GetName())
		return false // if we can't determine VM size, we assume it's not blocked
	}
	skuVersion := "1"
	if skuVMSize.Version != "" {
		if !(skuVMSize.Version[0] == 'V' || skuVMSize.Version[0] == 'v') {
			// should never happen; don't capture in label (won't be available for selection by version)
			return false
		}
		skuVersion = skuVMSize.Version[1:]
	}

	// Check if VM family is blocked in the specific zone
	if val, found := u.vmFamilyCache.Get(vmFamilyKey(skuVMSize.Family+skuVersion, zone, capacityType)); found {
		if blockedCPUCount, ok := val.(int64); ok {
			if blockedCPUCount == wholeVMFamilyBlockedSentinel {
				// Entire VM family is blocked in this zone
				return true
			}
			// VM sizes from this family are blocked for CPU counts >= blockedCPUCount in this zone
			return skuVCPUCount >= blockedCPUCount
		}
	}
	return false
}

// MarkFamilyUnavailableWithTTL marks a VM family with custom TTL in a specific zone
// versionedSKUFamily e.g. "N4" for "NV8as_v4"
func (u *UnavailableOfferings) MarkFamilyUnavailableWithTTL(ctx context.Context, versionedSKUFamily, zone, capacityType string, cpuCount int64, ttl time.Duration) {
	key := vmFamilyKey(versionedSKUFamily, zone, capacityType)

	if existing, found := u.vmFamilyCache.Get(key); found {
		if currentBlockedCPUCount, ok := existing.(int64); ok {
			// Keep the more restrictive limit for CPU count(lower value, with -1 being most restrictive - wholeVMFamilyBlockedSentinel)
			if currentBlockedCPUCount <= cpuCount {
				cpuCount = currentBlockedCPUCount
			}
		}
	}

	log.FromContext(ctx).WithValues(
		"family", versionedSKUFamily,
		"capacity-type", capacityType,
		"zone", zone,
		"max-cpu", cpuCount,
		"ttl", ttl).V(1).Info("marking VM family unavailable in zone")

	// call Set to update the cache entry, even if it already exists, to extend its TTL
	u.vmFamilyCache.Set(key, cpuCount, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkWholeVMFamilyUnavailable marks an entire VM family as unavailable in a specific zone
func (u *UnavailableOfferings) MarkWholeVMFamilyUnavailable(ctx context.Context, family, capacityType, zone string) {
	u.MarkFamilyUnavailableWithTTL(ctx, family, zone, capacityType, wholeVMFamilyBlockedSentinel, UnavailableOfferingsTTL)
}

// MarkVMFamilyUnavailableAtCPUCount marks a VM family as unavailable for CPU counts above the threshold in a specific zone
func (u *UnavailableOfferings) MarkVMFamilyUnavailableAtCPUCount(ctx context.Context, family, capacityType, zone string, maxCPUCount int64) {
	u.MarkFamilyUnavailableWithTTL(ctx, family, zone, capacityType, maxCPUCount, UnavailableOfferingsTTL)
}

// MarkSpotUnavailable communicates recently observed temporary capacity shortages for spot
func (u *UnavailableOfferings) MarkSpotUnavailableWithTTL(ctx context.Context, ttl time.Duration) {
	u.MarkUnavailableWithTTL(ctx, "SpotUnavailable", "", "", karpv1.CapacityTypeSpot, ttl)
}

// MarkUnavailableWithTTL allows us to mark an offering unavailable with a custom TTL
func (u *UnavailableOfferings) MarkUnavailableWithTTL(ctx context.Context, unavailableReason, instanceType, zone, capacityType string, ttl time.Duration) {
	// even if the key is already in the cache, we still need to call Set to extend the cached entry's TTL
	log.FromContext(ctx).V(1).Info("removing offering from offerings",
		"unavailable", unavailableReason,
		"instance-type", instanceType,
		"zone", zone,
		"capacity-type", capacityType,
		"ttl", ttl)
	u.singleOfferingCache.Set(singleInstanceKey(instanceType, zone, capacityType), struct{}{}, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkUnavailable communicates recently observed temporary capacity shortages in the provided offerings
func (u *UnavailableOfferings) MarkUnavailable(ctx context.Context, unavailableReason, instanceType, zone, capacityType string) {
	u.MarkUnavailableWithTTL(ctx, unavailableReason, instanceType, zone, capacityType, UnavailableOfferingsTTL)
}

func (u *UnavailableOfferings) Flush() {
	u.singleOfferingCache.Flush()
	u.vmFamilyCache.Flush()
	atomic.AddUint64(&u.SeqNum, 1)
}

// singleInstanceKey returns the cache singleInstanceKey for all offerings in the cache
func singleInstanceKey(instanceType string, zone string, capacityType string) string {
	return fmt.Sprintf("%s:%s:%s", capacityType, instanceType, zone)
}

// vmFamilyKey returns the cache key for VM family blocks in a specific zone
func vmFamilyKey(skuFamilyName, zone, capacityType string) string {
	return fmt.Sprintf("skuFamily:%s:%s:%s", skuFamilyName, zone, capacityType)
}
