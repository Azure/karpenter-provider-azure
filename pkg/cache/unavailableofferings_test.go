// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
)

func TestUnavailableOfferings(t *testing.T) {
	// create a new cache with a short TTL
	c := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(c)

	// test that an offering is not marked as unavailable initially
	if u.IsUnavailable("NV16as_v4", "westus", "spot") {
		t.Error("Offering should not be marked as unavailable initially")
	}

	// mark the offering as unavailable
	u.MarkUnavailableWithTTL(context.TODO(), "test reason", "NV16as_v4", "westus", "spot", time.Second)

	// test that the offering is now marked as unavailable
	if !u.IsUnavailable("NV16as_v4", "westus", "spot") {
		t.Error("Offering should be marked as unavailable after being marked as such")
	}

	// wait for the cache entry to expire
	time.Sleep(time.Second)

	// test that the offering is no longer marked as unavailable
	if u.IsUnavailable("NV16as_v4", "westus", "spot") {
		t.Error("Offering should not be marked as unavailable after cache entry has expired")
	}
}

func TestUnavailableOfferings_KeyGeneration(t *testing.T) {
	c := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(c)

	// test that the key is generated correctly
	expectedKey := "spot:NV16as_v4:westus"
	key := u.key("NV16as_v4", "westus", "spot")
	if key != expectedKey {
		t.Errorf("Expected key to be %s, but got %s", expectedKey, key)
	}
}
