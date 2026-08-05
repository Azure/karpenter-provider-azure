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
	spotKey = singleInstanceKey("", "", "", karpv1.CapacityTypeSpot)
)

// UnavailableOfferings stores any offerings that return ICE (insufficient capacity errors) when
// attempting to launch the capacity. These offerings are ignored as long as they are in the cache on
// GetInstanceTypes responses
// Information available from skewer.SKU is used to determine details about the VM SKU for which we encountered allocation errors.
type UnavailableOfferings struct {
	// TODO: I think this singleOfferingCache could basically be removed in favor of the family cache at this point, as we are now marking family unavailable at CPU count for all error cases.
	// I didn't do that purely because of the defensive fallback we have in markFamilyUnavailableAtCPUCountImpl, but in practice I am not sure if we'll ever have a nil family (I don't
	// see any evidence it can happen in logs)
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
	uo.singleOfferingCache.OnEvicted(func(_ string, _ any) {
		atomic.AddUint64(&uo.SeqNum, 1)
	})
	uo.vmFamilyCache.OnEvicted(func(_ string, _ any) {
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
	return u.ForCapacityReservationGroup("").IsUnavailable(sku, zone, capacityType)
}

// ForCapacityReservationGroup returns a view of the cache whose entries are namespaced to
// one capacity reservation group. An empty id yields the unreserved view.
//
// The two namespaces are kept apart in both directions because letting either leak into
// the other strands capacity silently, and for as long as the TTL: a group's launch
// failure would disable that size and zone for every other NodeClass, including ones that
// never touch the group, and a general capacity shortage would disable the reserved
// offering that exists to survive exactly that.
//
// Separating them does discard some real signal. A quota or allocation failure while
// overallocating a group is also evidence about unreserved capacity, and past the reserved
// quantity a general shortage really does predict a reserved launch failing. Each of those
// now costs one extra launch attempt, which then records itself in the correct scope.
// That is a better failure than silent stranding, and telling the two apart requires
// knowing the reserved quantity, which is what the bucket inventory is for.
func (u *UnavailableOfferings) ForCapacityReservationGroup(id string) *ScopedOfferings {
	return &ScopedOfferings{offerings: u, scope: strings.ToLower(id)}
}

// ScopedOfferings marks and queries unavailable offerings within one capacity reservation
// group, or outside any group when the scope is empty. It shares the underlying caches and
// sequence number with the UnavailableOfferings it came from.
type ScopedOfferings struct {
	offerings *UnavailableOfferings
	scope     string
}

// IsUnavailable returns true if the offering appears in the cache within this scope.
func (s *ScopedOfferings) IsUnavailable(sku *skewer.SKU, zone, capacityType string) bool {
	u := s.offerings
	// Spot is never reserved, so its unavailability is recorded and read unscoped.
	if capacityType == karpv1.CapacityTypeSpot {
		if _, found := u.singleOfferingCache.Get(spotKey); found {
			return true
		}
	}

	// check if the offering is marked as unavailable at vm family level
	if u.isFamilyUnavailable(s.scope, sku, zone, capacityType) {
		return true
	}

	// lastly check if the offering is marked as unavailable for the specific instance type, zone and capacity type
	_, found := u.singleOfferingCache.Get(singleInstanceKey(s.scope, sku.GetName(), zone, capacityType))
	return found
}

func (u *UnavailableOfferings) isFamilyUnavailable(scope string, sku *skewer.SKU, zone, capacityType string) bool {
	skuVCPUCount, err := sku.VCPU()
	if err != nil {
		// default to 0 if we can't determine VCPU count, this shouldn't happen as long as data in skewer.SKU is correct
		skuVCPUCount = 0
	}
	// Check if VM family is blocked in the specific zone
	if val, found := u.vmFamilyCache.Get(vmFamilyKey(scope, sku.GetFamilyName(), zone, capacityType)); found {
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

// markFamilyUnavailableAtCPUCount marks a VM family with custom TTL in a specific zone for all instance types that have CPU count at or above the SKU's vCPU count.
// Information is derived from the provided skewer.SKU: family name via GetFamilyName() and CPU count via VCPU().
func (u *UnavailableOfferings) markFamilyUnavailableAtCPUCount(ctx context.Context, scope string, sku *skewer.SKU, zone, capacityType string, ttl time.Duration) {
	cpuCount, err := sku.VCPU()
	if err != nil {
		// default to 0 if we can't determine VCPU count, this shouldn't happen as long as data in skewer.SKU is correct
		cpuCount = 0
	}
	u.markFamilyUnavailableAtCPUCountImpl(ctx, scope, sku, zone, capacityType, cpuCount, ttl)
}

// MarkFamilyUnavailable marks the entire VM family as unavailable in a specific zone for a specific capacity type with custom TTL.
// Family name is derived from the provided skewer.SKU.
func (u *UnavailableOfferings) MarkFamilyUnavailable(ctx context.Context, sku *skewer.SKU, zone, capacityType string, ttl time.Duration) {
	u.ForCapacityReservationGroup("").MarkFamilyUnavailable(ctx, sku, zone, capacityType, ttl)
}

// MarkFamilyUnavailable marks the entire VM family as unavailable within this scope.
func (s *ScopedOfferings) MarkFamilyUnavailable(ctx context.Context, sku *skewer.SKU, zone, capacityType string, ttl time.Duration) {
	s.offerings.markFamilyUnavailableAtCPUCountImpl(ctx, s.scope, sku, zone, capacityType, wholeVMFamilyBlockedSentinel, ttl)
}

// markFamilyUnavailableAtCPUCountImpl is the internal implementation that marks a VM family unavailable at a given CPU count threshold.
// Value of -1 is used as a "wholeVMFamilyBlockedSentinel" to indicate that the entire VM family is blocked in this zone for the specified capacity type.
func (u *UnavailableOfferings) markFamilyUnavailableAtCPUCountImpl(ctx context.Context, scope string, sku *skewer.SKU, zone, capacityType string, cpuCount int64, ttl time.Duration) {
	skuFamilyName := sku.GetFamilyName()
	// This is a hedge against skewer having bad data where family name is missing,
	// If family name is missing, we won't do any family level blocking, but we'll still mark the specific offering as unavailable.
	if skuFamilyName == "" {
		log.FromContext(ctx).V(0).Info("WARNING: cannot mark VM family as unavailable because SKU family name is missing",
			"instanceType", sku.GetName(),
			"zone", zone,
			"capacity-type", capacityType)
		return
	}
	key := vmFamilyKey(scope, skuFamilyName, zone, capacityType)

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

// MarkSpotUnavailable communicates recently observed temporary capacity shortages for spot
func (u *UnavailableOfferings) MarkSpotUnavailableWithTTL(ctx context.Context, ttl time.Duration) {
	capacityType := karpv1.CapacityTypeSpot
	// even if the key is already in the cache, we still need to call Set to extend the cached entry's TTL
	log.FromContext(ctx).V(1).Info("removing offering from offerings",
		"unavailable", "SpotUnavailable",
		"capacity-type", capacityType,
		"ttl", ttl)
	u.singleOfferingCache.Set(spotKey, struct{}{}, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkSpotUnavailableWithTTL records a spot shortage. Spot cannot consume a capacity
// reservation, so this is always recorded unscoped.
func (s *ScopedOfferings) MarkSpotUnavailableWithTTL(ctx context.Context, ttl time.Duration) {
	s.offerings.MarkSpotUnavailableWithTTL(ctx, ttl)
}

// MarkUnavailableWithTTL allows us to mark an offering unavailable with a custom TTL.
// In addition to marking the specific instance type unavailable, it also marks the VM family
// unavailable at the SKU's vCPU count, so that larger sizes of the same family are also blocked.
func (u *UnavailableOfferings) MarkUnavailableWithTTL(ctx context.Context, unavailableReason string, sku *skewer.SKU, zone, capacityType string, ttl time.Duration) {
	u.ForCapacityReservationGroup("").MarkUnavailableWithTTL(ctx, unavailableReason, sku, zone, capacityType, ttl)
}

// MarkUnavailableWithTTL marks an offering unavailable within this scope.
func (s *ScopedOfferings) MarkUnavailableWithTTL(ctx context.Context, unavailableReason string, sku *skewer.SKU, zone, capacityType string, ttl time.Duration) {
	u := s.offerings
	instanceType := sku.GetName()
	// even if the key is already in the cache, we still need to call Set to extend the cached entry's TTL
	log.FromContext(ctx).V(1).Info("removing offering from offerings",
		"unavailable", unavailableReason,
		logging.InstanceType, instanceType,
		"zone", zone,
		"capacity-type", capacityType,
		"capacity-reservation-group", s.scope,
		"ttl", ttl)
	u.singleOfferingCache.Set(singleInstanceKey(s.scope, instanceType, zone, capacityType), struct{}{}, ttl)
	atomic.AddUint64(&u.SeqNum, 1)

	// Also mark the VM family unavailable at this SKU's vCPU count, so larger sizes of the same family are blocked too
	u.markFamilyUnavailableAtCPUCount(ctx, s.scope, sku, zone, capacityType, ttl)
}

// MarkUnavailable communicates recently observed temporary capacity shortages in the provided offerings
func (u *UnavailableOfferings) MarkUnavailable(ctx context.Context, unavailableReason string, sku *skewer.SKU, zone, capacityType string) {
	u.MarkUnavailableWithTTL(ctx, unavailableReason, sku, zone, capacityType, UnavailableOfferingsTTL)
}

// MarkUnavailable communicates recently observed temporary capacity shortages within this scope.
func (s *ScopedOfferings) MarkUnavailable(ctx context.Context, unavailableReason string, sku *skewer.SKU, zone, capacityType string) {
	s.MarkUnavailableWithTTL(ctx, unavailableReason, sku, zone, capacityType, UnavailableOfferingsTTL)
}

func (u *UnavailableOfferings) Flush() {
	u.singleOfferingCache.Flush()
	u.vmFamilyCache.Flush()
	atomic.AddUint64(&u.SeqNum, 1)
}

// singleInstanceKey returns the cache singleInstanceKey for all offerings in the cache
func singleInstanceKey(scope, instanceType string, zone string, capacityType string) string {
	if scope == "" {
		return fmt.Sprintf("%s:%s:%s", capacityType, instanceType, zone)
	}
	return fmt.Sprintf("crg:%s:%s:%s:%s", scope, capacityType, instanceType, zone)
}

// vmFamilyKey returns the cache key for VM family blocks in a specific zone
func vmFamilyKey(scope, skuFamilyName, zone, capacityType string) string {
	if scope == "" {
		return strings.ToLower(fmt.Sprintf("skuFamily:%s:%s:%s", skuFamilyName, zone, capacityType))
	}
	return strings.ToLower(fmt.Sprintf("crg:%s:skuFamily:%s:%s:%s", scope, skuFamilyName, zone, capacityType))
}
