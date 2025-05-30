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

	"github.com/patrickmn/go-cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var (
	spotKey = key("", "", karpv1.CapacityTypeSpot)
)

const (
	// wholeVMFamilyBlockedSentinel means that entire SKU family is blocked, not just certain instance types within it (we might block certain instance types that have more than specific amount of CPUs, but allow others)
	wholeVMFamilyBlockedSentinel = -1
)

// UnavailableOfferings stores any offerings that return ICE (insufficient capacity errors) when
// attempting to launch the capacity. These offerings are ignored as long as they are in the cache on
// GetInstanceTypes responses
type UnavailableOfferings struct {
	// key: <capacityType>:<instanceType>:<zone>, value: struct{}{} - for single instance types
	// key: skuFamily:<capacityType>:<versionedSKUFamily>:<zone>, value: int64 - for VM families, where int64 is the maximum CPU count allowed for this family in this zone (or -1 if the entire family is unavailable)
	cache  *cache.Cache
	SeqNum uint64
}

func NewUnavailableOfferingsWithCache(c *cache.Cache) *UnavailableOfferings {
	uo := &UnavailableOfferings{
		cache:  c,
		SeqNum: 0,
	}
	uo.cache.OnEvicted(func(_ string, _ interface{}) {
		atomic.AddUint64(&uo.SeqNum, 1)
	})
	return uo
}

func NewUnavailableOfferings() *UnavailableOfferings {
	return NewUnavailableOfferingsWithCache(
		cache.New(UnavailableOfferingsTTL, UnavailableOfferingsCleanupInterval))
}

// IsUnavailable returns true if the offering appears in the cache
func (u *UnavailableOfferings) IsUnavailable(instanceType, versionedSKUFamily, zone, capacityType string, cpuCount int64) bool {
	// first check if the offering is marked as unavailable for spot capacity - sometimes we blanket mark all spot offerings as unavailable
	if capacityType == karpv1.CapacityTypeSpot {
		if _, found := u.cache.Get(spotKey); found {
			return true
		}
	}

	// check if there offering is marked as unavailable on vm family level
	if u.IsFamilyUnavailable(versionedSKUFamily, capacityType, zone, cpuCount) {
		return true
	}

	// lastly check if the offering is marked as unavailable for the specific instance type, zone and capacity type
	_, found := u.cache.Get(key(instanceType, zone, capacityType))
	return found
}

// MarkSpotUnavailable communicates recently observed temporary capacity shortages for spot
func (u *UnavailableOfferings) MarkSpotUnavailableWithTTL(ctx context.Context, ttl time.Duration) {
	u.MarkUnavailableWithTTL(ctx, "SpotUnavailable", "", "", karpv1.CapacityTypeSpot, ttl)
}

// MarkUnavailableWithTTL allows us to mark an offering unavailable with a custom TTL
func (u *UnavailableOfferings) MarkUnavailableWithTTL(ctx context.Context, unavailableReason, instanceType, zone, capacityType string, ttl time.Duration) {
	// even if the key is already in the cache, we still need to call Set to extend the cached entry's TTL
	log.FromContext(ctx).WithValues(
		"unavailable", unavailableReason,
		"instance-type", instanceType,
		"zone", zone,
		"capacity-type", capacityType,
		"ttl", ttl).V(1).Info("removing offering from offerings")
	u.cache.Set(key(instanceType, zone, capacityType), struct{}{}, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkUnavailable communicates recently observed temporary capacity shortages in the provided offerings
func (u *UnavailableOfferings) MarkUnavailable(ctx context.Context, unavailableReason, instanceType, zone, capacityType string) {
	u.MarkUnavailableWithTTL(ctx, unavailableReason, instanceType, zone, capacityType, UnavailableOfferingsTTL)
}

func (u *UnavailableOfferings) IsFamilyUnavailable(versionedSKUFamily, capacityType, zone string, cpuCount int64) bool {
	// Check if VM family is blocked in the specific zone
	if val, found := u.cache.Get(skuFamilyKey(versionedSKUFamily, capacityType, zone)); found {
		if maxCPU, ok := val.(int64); ok {
			if maxCPU == wholeVMFamilyBlockedSentinel {
				// Entire VM family is blocked in this zone
				return true
			}
			// VM sizes from this family are blocked for CPU counts >= maxCPU in this zone
			return cpuCount >= maxCPU
		}
	}
	return false
}

// MarkFamilyUnavailableWithTTL marks a VM family with custom TTL in a specific zone
func (u *UnavailableOfferings) MarkFamilyUnavailableWithTTL(ctx context.Context, versionedSKUFamily, capacityType, zone string, maxAllowedCPUCount int64, ttl time.Duration) {
	key := skuFamilyKey(versionedSKUFamily, capacityType, zone)

	// Check if we already have a more restrictive limit
	if existing, found := u.cache.Get(key); found {
		if existingMaxAllowedCPUCount, ok := existing.(int64); ok {
			// If entire family is already blocked, don't override
			if existingMaxAllowedCPUCount == wholeVMFamilyBlockedSentinel {
				return
			}
			// If new limit would be less restrictive, keep the existing one
			if maxAllowedCPUCount != wholeVMFamilyBlockedSentinel && existingMaxAllowedCPUCount <= maxAllowedCPUCount {
				return
			}
		}
	}

	log.FromContext(ctx).WithValues(
		"family", versionedSKUFamily,
		"capacity-type", capacityType,
		"zone", zone,
		"max-cpu", maxAllowedCPUCount,
		"ttl", ttl).V(1).Info("marking VM family unavailable in zone")

	u.cache.Set(key, maxAllowedCPUCount, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkWholeVMFamilyUnavailable marks an entire VM family as unavailable in a specific zone
func (u *UnavailableOfferings) MarkWholeVMFamilyUnavailable(ctx context.Context, family, capacityType, zone string) {
	u.MarkFamilyUnavailableWithTTL(ctx, family, capacityType, zone, wholeVMFamilyBlockedSentinel, UnavailableOfferingsTTL)
}

// MarkVMFamilyUnavailableAtCPUCount marks a VM family as unavailable for CPU counts above the threshold in a specific zone
func (u *UnavailableOfferings) MarkVMFamilyUnavailableAtCPUCount(ctx context.Context, family, capacityType, zone string, maxCPUCount int64) {
	u.MarkFamilyUnavailableWithTTL(ctx, family, capacityType, zone, maxCPUCount, UnavailableOfferingsTTL)
}

func (u *UnavailableOfferings) Flush() {
	u.cache.Flush()
	atomic.AddUint64(&u.SeqNum, 1)
}

// key returns the cache key for all offerings in the cache
func key(instanceType string, zone string, capacityType string) string {
	return fmt.Sprintf("%s:%s:%s", capacityType, instanceType, zone)
}

// skuFamilyKey returns the cache key for VM family blocks in a specific zone
func skuFamilyKey(versionedSKUFamily, capacityType, zone string) string {
	return fmt.Sprintf("skuFamily:%s:%s:%s", capacityType, versionedSKUFamily, zone)
}
