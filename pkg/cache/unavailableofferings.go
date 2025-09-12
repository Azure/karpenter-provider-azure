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
	"strings"
	"sync/atomic"
	"time"

	"github.com/Azure/skewer"
	"github.com/patrickmn/go-cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/logging"
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
// We maintain two caches (singleOfferingCache and vmFamilyCache) to better handle error cases in which we know that allocation from a specific VM family + capacity type + zone combination will not work.
// Information available from skewer.SKU is used to determine details about the VM SKU for which we encountered allocation errors.
// We don't bundle the two caches together into one to avoid accidentally bundling together different SKUs while handling errors which don't necessarily warrant blocking more than just the single instance type.
// This could be adjusted in the future, as we gather more data and get more confidence in information available in skewer.SKU.
type UnavailableOfferings struct {
	// key: <capacityType>:<instanceType>:<zone>, value: struct{}{}
	singleOfferingCache *cache.Cache
	// key: <skuFamilyName>:<zone>:<capacityType> (lowercase), value: int64 (CPU count at or above which we block, or wholeVMFamilyBlockedSentinel if entire family is blocked)
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
		// default to 0 if we can't determine VCPU count, this shouldn't happen as long as data in skewer.SKU is correct
		skuVCPUCount = 0
	}
	// Check if VM family is blocked in the specific zone
	if val, found := u.vmFamilyCache.Get(vmFamilyKey(sku.GetFamilyName(), zone, capacityType)); found {
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

// MarkFamilyUnavailableAtCPUCount marks a VM family with custom TTL in a specific zone for all instance types that have CPU count at or above the provided cpuCount
// Value of -1 is used as a "wholeVMFamilyBlockedSentinel" to indicate that the entire VM family is blocked in this zone for the specified capacity type.
// skuFamilyName e.g. "StandardDv2Family" for "Standard_D2_v2" VM SKU
func (u *UnavailableOfferings) MarkFamilyUnavailableAtCPUCount(ctx context.Context, skuFamilyName, zone, capacityType string, cpuCount int64, ttl time.Duration) {
	key := vmFamilyKey(skuFamilyName, zone, capacityType)

	if existing, found := u.vmFamilyCache.Get(key); found {
		if currentBlockedCPUCount, ok := existing.(int64); ok {
			// Keep the more restrictive limit for CPU count(lower value, with -1 being most restrictive - wholeVMFamilyBlockedSentinel)
			if currentBlockedCPUCount <= cpuCount {
				cpuCount = currentBlockedCPUCount
			}
		}
	}

	log.FromContext(ctx).V(1).Info("marking VM Family unavailable in zone",
		"family", skuFamilyName,
		"capacity-type", capacityType,
		"zone", zone,
		"max-cpu", cpuCount,
		"ttl", ttl)

	// call Set to update the cache entry, even if it already exists, to extend its TTL
	u.vmFamilyCache.Set(key, cpuCount, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkFamilyUnavailable marks the entire VM family as unavailable in a specific zone for a specific capacity type with custom TTL
func (u *UnavailableOfferings) MarkFamilyUnavailable(ctx context.Context, skuFamilyName, zone, capacityType string, ttl time.Duration) {
	u.MarkFamilyUnavailableAtCPUCount(ctx, skuFamilyName, zone, capacityType, wholeVMFamilyBlockedSentinel, ttl)
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
		logging.InstanceType, instanceType,
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
	return strings.ToLower(fmt.Sprintf("skuFamily:%s:%s:%s", skuFamilyName, zone, capacityType))
}
