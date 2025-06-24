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

// UnavailableOfferings stores any offerings that return ICE (insufficient capacity errors) when
// attempting to launch the capacity. These offerings are ignored as long as they are in the cache on
// GetInstanceTypes responses
type UnavailableOfferings struct {
	// key: <capacityType>:<instanceType>:<zone>, value: struct{}{}
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
func (u *UnavailableOfferings) IsUnavailable(instanceType, zone, capacityType string) bool {
	if capacityType == karpv1.CapacityTypeSpot {
		if _, found := u.cache.Get(spotKey); found {
			return true
		}
	}
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
	log.FromContext(ctx).V(1).Info("removing offering from offerings",
		"unavailable", unavailableReason,
		"instance-type", instanceType,
		"zone", zone,
		"capacity-type", capacityType,
		"ttl", ttl)
	u.cache.Set(key(instanceType, zone, capacityType), struct{}{}, ttl)
	atomic.AddUint64(&u.SeqNum, 1)
}

// MarkUnavailable communicates recently observed temporary capacity shortages in the provided offerings
func (u *UnavailableOfferings) MarkUnavailable(ctx context.Context, unavailableReason, instanceType, zone, capacityType string) {
	u.MarkUnavailableWithTTL(ctx, unavailableReason, instanceType, zone, capacityType, UnavailableOfferingsTTL)
}

func (u *UnavailableOfferings) Flush() {
	u.cache.Flush()
	atomic.AddUint64(&u.SeqNum, 1)
}

// key returns the cache key for all offerings in the cache
func key(instanceType string, zone string, capacityType string) string {
	return fmt.Sprintf("%s:%s:%s", capacityType, instanceType, zone)
}
