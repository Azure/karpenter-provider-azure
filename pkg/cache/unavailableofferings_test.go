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

	"github.com/patrickmn/go-cache"
)

func assertOfferingUnavailable(t *testing.T, u *UnavailableOfferings, instanceType, versionedSKUFamily, zone string, cpuCoreCount int64, message string) {
	t.Helper()
	if !u.IsUnavailable(instanceType, versionedSKUFamily, zone, "spot", cpuCoreCount) {
		t.Errorf("%s: %s", instanceType, message)
	}
}

func assertOfferingAvailable(t *testing.T, u *UnavailableOfferings, instanceType, versionedSKUFamily, zone string, cpuCoreCount int64, message string) {
	t.Helper()
	if u.IsUnavailable(instanceType, versionedSKUFamily, zone, "spot", cpuCoreCount) {
		t.Errorf("%s: %s", instanceType, message)
	}
}

func TestUnavailableOfferings(t *testing.T) {
	// create a new cache with a short TTL
	c := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(c)

	// test that an offering is not marked as unavailable initially
	assertOfferingAvailable(t, u, "D2s_v3", "D3", "westus-1", 2, "should not be marked as unavailable initially")

	// mark the offering as unavailable
	u.MarkUnavailableWithTTL(context.TODO(), "test reason", "D2s_v3", "westus-1", "spot", time.Second)

	// test that the offering is now marked as unavailable
	assertOfferingUnavailable(t, u, "D2s_v3", "D3", "westus-1", 2, "should be marked as unavailable after being marked as such")

	// test that the same VM SKU in a different zone is still available
	assertOfferingAvailable(t, u, "D2s_v3", "D3", "westus-2", 2, "should be available in a different zone")

	// wait for the cache entry to expire
	time.Sleep(time.Second)

	// test that the offering is no longer marked as unavailable
	assertOfferingAvailable(t, u, "D2s_v3", "D3", "westus-1", 2, "should not be marked as unavailable after cache entry has expired")
}

func TestUnavailableOfferingsVMFamilyLevel(t *testing.T) {
	// create a new cache with a short TTL
	c := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(c)

	// Test that offerings are not marked as unavailable initially
	assertOfferingAvailable(t, u, "NV16as_v4", "NV4", "westus-1", 16, "16 CPU offering should not be marked as unavailable initially")
	assertOfferingAvailable(t, u, "NV24as_v4", "NV4", "westus-1", 24, "24 CPU offering should not be marked as unavailable initially")
	assertOfferingAvailable(t, u, "NV8as_v4", "NV4", "westus-1", 8, "8 CPU offering should not be marked as unavailable initially")

	// Mark part of the VM family as unavailable (16+ CPUs)
	u.MarkFamilyUnavailableWithTTL(context.TODO(), "NV4", "spot", "westus-1", 16, time.Second)

	// Test that 16+ CPU offerings are marked as unavailable, but 8 CPU is not
	assertOfferingUnavailable(t, u, "NV16as_v4", "NV4", "westus-1", 16, "16 CPU offering should be unavailable after partial family marking")
	assertOfferingUnavailable(t, u, "NV24as_v4", "NV4", "westus-1", 24, "24 CPU offering should be unavailable after partial family marking")
	assertOfferingAvailable(t, u, "NV8as_v4", "NV4", "westus-1", 8, "8 CPU offering should remain available after partial family marking")

	// Wait for cache expiration
	time.Sleep(time.Second)

	// Test that offerings are no longer marked as unavailable after expiration
	assertOfferingAvailable(t, u, "NV16as_v4", "NV4", "westus-1", 16, "16 CPU offering should not be marked as unavailable after cache expiration")
	assertOfferingAvailable(t, u, "NV24as_v4", "NV4", "westus-1", 24, "24 CPU offering should not be marked as unavailable after cache expiration")
	assertOfferingAvailable(t, u, "NV8as_v4", "NV4", "westus-1", 8, "8 CPU offering should not be marked as unavailable after cache expiration")

	// Mark the entire VM family as unavailable
	u.MarkFamilyUnavailableWithTTL(context.TODO(), "NV4", "spot", "westus-1", -1, time.Second)

	// Test that offerings with both more and fewer than 16 CPUs are now marked as unavailable
	assertOfferingUnavailable(t, u, "NV16as_v4", "NV4", "westus-1", 16, "16 CPU offering should be unavailable after entire family marking")
	assertOfferingUnavailable(t, u, "NV24as_v4", "NV4", "westus-1", 24, "24 CPU offering should be unavailable after entire family marking")
	assertOfferingUnavailable(t, u, "NV8as_v4", "NV4", "westus-1", 8, "8 CPU offering should be unavailable after entire family marking")

	// Test that offering from same VM family and version but in a different zone are available
	assertOfferingAvailable(t, u, "NV16as_v4", "NV4", "westus-2", 16, "Offering in a different zone should be available")

	// Test that offerings from same VM family but different version are available
	assertOfferingAvailable(t, u, "NV16as_v5", "NV5", "westus-1", 16, "Offering from a different version of the same VM family should be available")

	// Wait for cache expiration
	time.Sleep(time.Second)

	// // Test that offerings with both more and fewer than 16 CPUs are now marked as available after expiration
	assertOfferingAvailable(t, u, "NV16as_v4", "NV4", "westus-1", 16, "16 CPU offering should not be marked as unavailable after cache expiration")
	assertOfferingAvailable(t, u, "NV24as_v4", "NV4", "westus-1", 24, "24 CPU offering should not be marked as unavailable after cache expiration")
	assertOfferingAvailable(t, u, "NV8as_v4", "NV4", "westus-1", 8, "8 CPU offering should not be marked as unavailable after cache expiration")
}

func TestUnavailableOfferings_KeyGeneration(t *testing.T) {
	expectedKey := "spot:NV16as_v4:westus-1"
	key := key("NV16as_v4", "westus-1", "spot")
	if key != expectedKey {
		t.Errorf("Expected key to be %s, but got %s", expectedKey, key)
	}
}
