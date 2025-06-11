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
	"testing"
	"time"

	"github.com/Azure/skewer"
	"github.com/patrickmn/go-cache"
)

func createTestSKU(name, family string) *skewer.SKU {
	return &skewer.SKU{
		Name:   &name,
		Family: &family,
	}
}

func TestUnavailableOfferings(t *testing.T) {
	// create a new cache with a short TTL
	singleInstanceCache := cache.New(time.Second, time.Second)
	vmFamilyCache := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)
	testSKU := createTestSKU("NV16as_v4", "NVas_v4")

	// test that an offering is not marked as unavailable initially
	if u.IsUnavailable(testSKU, "westus", "spot") {
		t.Error("Offering should not be marked as unavailable initially")
	}

	// mark the offering as unavailable
	u.MarkUnavailableWithTTL(context.TODO(), "test reason", "NV16as_v4", "westus", "spot", time.Second)

	// test that the offering is now marked as unavailable
	if !u.IsUnavailable(testSKU, "westus", "spot") {
		t.Error("Offering should be marked as unavailable after being marked as such")
	}

	// wait for the cache entry to expire
	time.Sleep(time.Second)

	// test that the offering is no longer marked as unavailable
	if u.IsUnavailable(testSKU, "westus", "spot") {
		t.Error("Offering should not be marked as unavailable after cache entry has expired")
	}
}

func TestUnavailableOfferings_KeyGeneration(t *testing.T) {
	expectedKey := "spot:NV16as_v4:westus"
	key := singleInstanceKey("NV16as_v4", "westus", "spot")
	if key != expectedKey {
		t.Errorf("Expected key to be %s, but got %s", expectedKey, key)
	}
}
