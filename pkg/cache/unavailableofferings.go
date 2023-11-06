// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"
	"knative.dev/pkg/logging"
)

// UnavailableOfferings stores any offerings that return ICE (insufficient capacity errors) when
// attempting to launch the capacity. These offerings are ignored as long as they are in the cache on
// GetInstanceTypes responses
type UnavailableOfferings struct {
	// key: <capacityType>:<instanceType>:<zone>, value: struct{}{}
	cache *cache.Cache
}

func NewUnavailableOfferingsWithCache(c *cache.Cache) *UnavailableOfferings {
	return &UnavailableOfferings{
		cache: c,
	}
}

func NewUnavailableOfferings() *UnavailableOfferings {
	c := cache.New(UnavailableOfferingsTTL, DefaultCleanupInterval)
	return &UnavailableOfferings{
		cache: c,
	}
}

// IsUnavailable returns true if the offering appears in the cache
func (u *UnavailableOfferings) IsUnavailable(instanceType, zone, capacityType string) bool {
	_, found := u.cache.Get(u.key(instanceType, zone, capacityType))
	return found
}

// MarkUnavailableWithTTL allows us to mark an offering unavailable with a custom TTL
func (u *UnavailableOfferings) MarkUnavailableWithTTL(ctx context.Context, unavailableReason, instanceType, zone, capacityType string, ttl time.Duration) {
	// even if the key is already in the cache, we still need to call Set to extend the cached entry's TTL
	logging.FromContext(ctx).With(
		"unavailable", unavailableReason,
		"instance-type", instanceType,
		"zone", zone,
		"capacity-type", capacityType,
		"ttl", ttl).Debugf("removing offering from offerings")
	u.cache.Set(u.key(instanceType, zone, capacityType), struct{}{}, ttl)
}

// MarkUnavailable communicates recently observed temporary capacity shortages in the provided offerings
func (u *UnavailableOfferings) MarkUnavailable(ctx context.Context, unavailableReason, instanceType, zone, capacityType string) {
	u.MarkUnavailableWithTTL(ctx, unavailableReason, instanceType, zone, capacityType, UnavailableOfferingsTTL)
}

func (u *UnavailableOfferings) Flush() {
	u.cache.Flush()
}

// key returns the cache key for all offerings in the cache
func (u *UnavailableOfferings) key(instanceType string, zone string, capacityType string) string {
	return fmt.Sprintf("%s:%s:%s", capacityType, instanceType, zone)
}
