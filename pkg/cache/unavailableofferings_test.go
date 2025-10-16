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
	"strconv"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/skewer/v2"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	testUnavailableOfferingsTTL = 2 * time.Second
)

func createTestSKU(name, familyName, size string, cpuCount int) *skewer.SKU {
	return &skewer.SKU{
		Name:   &name,
		Family: &familyName,
		Size:   &size,
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{
				Name:  lo.ToPtr(skewer.VCPUs),
				Value: lo.ToPtr(strconv.Itoa(cpuCount)),
			},
		},
	}
}

func assertOfferingUnavailable(t *testing.T, u *UnavailableOfferings, sku *skewer.SKU, zone, capacityType, message string) {
	t.Helper()
	if !u.IsUnavailable(sku, zone, capacityType) {
		t.Errorf("%s: %s", sku.GetName(), message)
	}
}

func assertOfferingAvailable(t *testing.T, u *UnavailableOfferings, sku *skewer.SKU, zone, capacityType, message string) {
	t.Helper()
	if u.IsUnavailable(sku, zone, capacityType) {
		t.Errorf("%s: %s", sku.GetName(), message)
	}
}

func TestUnavailableOfferings(t *testing.T) {
	// create a new cache with a short TTL
	singleInstanceCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	vmFamilyCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)
	testSKU := createTestSKU("Standard_NV16as_v4", "standardNVasv4Family", "NV16as_v4", 16)

	// test that an offering is not marked as unavailable initially
	assertOfferingAvailable(t, u, testSKU, "westus", karpv1.CapacityTypeSpot, "Offering should not be marked as unavailable initially")

	// mark the offering as unavailable
	u.MarkUnavailableWithTTL(context.TODO(), "test reason", "Standard_NV16as_v4", "westus", karpv1.CapacityTypeSpot, testUnavailableOfferingsTTL)

	// test that the offering is now marked as unavailable
	assertOfferingUnavailable(t, u, testSKU, "westus", karpv1.CapacityTypeSpot, "Offering should be marked as unavailable")
	// test that offering is available for different capacity type
	assertOfferingAvailable(t, u, testSKU, "westus", karpv1.CapacityTypeOnDemand, "Offering should be available for different capacity type")

	// wait for the cache entry to expire
	time.Sleep(testUnavailableOfferingsTTL)

	// test that the offering is no longer marked as unavailable
	assertOfferingAvailable(t, u, testSKU, "westus", karpv1.CapacityTypeSpot, "Offering should not be marked as unavailable after cache entry has expired")
}

func TestUnavailableOfferingsVMFamilyCoreLimitAllowsFewerCores(t *testing.T) {
	// create a new cache with a short TTL
	singleInstanceCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	vmFamilyCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)

	nv8 := createTestSKU("Standard_NV8as_v4", "standardNVasv4Family", "NV8as_v4", 8)
	nv16 := createTestSKU("Standard_NV16as_v4", "standardNVasv4Family", "NV16as_v4", 16)
	nv24 := createTestSKU("Standard_NV24as_v4", "standardNVasv4Family", "NV24as_v4", 24)
	skus := []*skewer.SKU{nv8, nv16, nv24}

	// Test that offerings are not marked as unavailable initially
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable initially")
	}

	// Mark part of the VM family as unavailable (>= 16 CPUs)
	u.MarkFamilyUnavailableAtCPUCount(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, 16, testUnavailableOfferingsTTL)

	// Test that 16+ CPU offerings are marked as unavailable, but 8 CPU is not
	assertOfferingAvailable(t, u, nv8, "westus-1", karpv1.CapacityTypeOnDemand, "8 CPU offering should remain available after partial family marking")
	assertOfferingUnavailable(t, u, nv16, "westus-1", karpv1.CapacityTypeOnDemand, "16 CPU offering should be unavailable after partial family marking")
	assertOfferingUnavailable(t, u, nv24, "westus-1", karpv1.CapacityTypeOnDemand, "24 CPU offering should be unavailable after partial family marking")

	// Wait for cache expiration
	time.Sleep(testUnavailableOfferingsTTL)

	// Test that offerings are no longer marked as unavailable after expiration
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable after cache expiration")
	}
}

func TestUnavailableOfferingsVMFamilyBlocksAll(t *testing.T) {
	// create a new cache with a short TTL
	singleInstanceCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	vmFamilyCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)

	nv8 := createTestSKU("Standard_NV8as_v4", "standardNVasv4Family", "NV8as_v4", 8)
	nv16 := createTestSKU("Standard_NV16as_v4", "standardNVasv4Family", "NV16as_v4", 16)
	nv24 := createTestSKU("Standard_NV24as_v4", "standardNVasv4Family", "NV24as_v4", 24)
	skus := []*skewer.SKU{nv8, nv16, nv24}

	// Test that offerings are not marked as unavailable initially
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable initially")
	}

	// Mark the entire VM family as unavailable
	u.MarkFamilyUnavailable(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, testUnavailableOfferingsTTL)

	// Test that offerings all offerings from the VM family are marked as unavailable in specific zone
	for _, sku := range skus {
		assertOfferingUnavailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should be marked as unavailable after entire family marking")
	}

	// Test that offering from same VM family and version but in a different zone are available
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-2", karpv1.CapacityTypeOnDemand, "Offering in a different zone should be available")
	}

	// Test that offerings from same VM family but different version are available
	differentVersionSKU := createTestSKU("Standard_NV16as_v5", "standardNVasv5Family", "NV16as_v5", 16)
	assertOfferingAvailable(t, u, differentVersionSKU, "westus-1", karpv1.CapacityTypeOnDemand, "Offering from a different version of the same VM family should be available")

	// Wait for cache expiration
	time.Sleep(testUnavailableOfferingsTTL)

	// Test that all offerings are now marked as available after expiration
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable after cache expiration")
	}
}

func TestUnavailableOfferings_KeyGeneration(t *testing.T) {
	expectedKey := "spot:NV16as_v4:westus"
	key := singleInstanceKey("NV16as_v4", "westus", "spot")
	if key != expectedKey {
		t.Errorf("Expected key to be %s, but got %s", expectedKey, key)
	}
}

func TestUnavailableOfferingsRestrictiveLimitPreservation(t *testing.T) {
	// create a new cache with a long TTL to avoid expiration during test
	singleInstanceCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	vmFamilyCache := cache.New(testUnavailableOfferingsTTL, testUnavailableOfferingsTTL)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)

	nv8 := createTestSKU("Standard_NV8as_v4", "standardNVasv4Family", "NV8as_v4", 8)
	nv16 := createTestSKU("Standard_NV16as_v4", "standardNVasv4Family", "NV16as_v4", 16)
	nv24 := createTestSKU("Standard_NV24as_v4", "standardNVasv4Family", "NV24as_v4", 24)
	nv32 := createTestSKU("Standard_NV32as_v4", "standardNVasv4Family", "NV32as_v4", 32)
	skus := []*skewer.SKU{nv8, nv16, nv24, nv32}

	// Test 1: Set a limit at 16 CPUs, then try to set a less restrictive limit at 24 CPUs
	// The 16 CPU limit should be preserved
	u.MarkFamilyUnavailableAtCPUCount(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, 16, testUnavailableOfferingsTTL)

	// Verify initial state: 8 CPU available, 16+ CPUs unavailable
	assertOfferingAvailable(t, u, nv8, "westus-1", karpv1.CapacityTypeOnDemand, "8 CPU offering should be available with 16 CPU limit")
	assertOfferingUnavailable(t, u, nv16, "westus-1", karpv1.CapacityTypeOnDemand, "16 CPU offering should be unavailable with 16 CPU limit")
	assertOfferingUnavailable(t, u, nv24, "westus-1", karpv1.CapacityTypeOnDemand, "24 CPU offering should be unavailable with 16 CPU limit")

	// Try to set a less restrictive limit (24 CPUs) - should preserve the 16 CPU limit
	u.MarkFamilyUnavailableAtCPUCount(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, 24, testUnavailableOfferingsTTL)

	// Verify the 16 CPU limit is preserved
	assertOfferingAvailable(t, u, nv8, "westus-1", karpv1.CapacityTypeOnDemand, "8 CPU offering should remain available after less restrictive limit attempt")
	assertOfferingUnavailable(t, u, nv16, "westus-1", karpv1.CapacityTypeOnDemand, "16 CPU offering should remain unavailable after less restrictive limit attempt")
	assertOfferingUnavailable(t, u, nv24, "westus-1", karpv1.CapacityTypeOnDemand, "24 CPU offering should remain unavailable after less restrictive limit attempt")

	// Test 2: Set a more restrictive limit (8 CPUs) - should override the 16 CPU limit
	u.MarkFamilyUnavailableAtCPUCount(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, 8, testUnavailableOfferingsTTL)

	// Verify the 8 CPU limit is now in effect
	assertOfferingUnavailable(t, u, nv8, "westus-1", karpv1.CapacityTypeOnDemand, "8 CPU offering should be unavailable with 8 CPU limit")
	assertOfferingUnavailable(t, u, nv16, "westus-1", karpv1.CapacityTypeOnDemand, "16 CPU offering should be unavailable with 8 CPU limit")
	assertOfferingUnavailable(t, u, nv24, "westus-1", karpv1.CapacityTypeOnDemand, "24 CPU offering should be unavailable with 8 CPU limit")

	// Test 3: Set whole family blocked (-1) - should override any CPU limit
	u.MarkFamilyUnavailable(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, testUnavailableOfferingsTTL)

	// Verify all offerings are unavailable
	for _, sku := range skus {
		assertOfferingUnavailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "All offerings should be unavailable when whole family is blocked")
	}

	// Test 4: Try to set a less restrictive limit after whole family is blocked - should preserve -1
	u.MarkFamilyUnavailableAtCPUCount(context.TODO(), nv8.GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, 32, testUnavailableOfferingsTTL)

	// Verify all offerings remain unavailable
	for _, sku := range skus {
		assertOfferingUnavailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "All offerings should remain unavailable after attempting to override whole family block")
	}

	// Wait for cache expiration
	time.Sleep(testUnavailableOfferingsTTL)

	// Verify all offerings are available again after expiration
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable after cache expiration")
	}
}
