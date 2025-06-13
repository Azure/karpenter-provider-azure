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

	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/skewer"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func createTestSKU(name, family string, cpuCount int) *skewer.SKU {
	return &skewer.SKU{
		Name:         &name,
		Family:       &family,
		Capabilities: &[]compute.ResourceSkuCapabilities{{Name: lo.ToPtr(skewer.VCPUs), Value: lo.ToPtr(strconv.Itoa(cpuCount))}},
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
	singleInstanceCache := cache.New(time.Second, time.Second)
	vmFamilyCache := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)
	testSKU := createTestSKU("NV16as_v4", "NVasv4Family", 16)

	// test that an offering is not marked as unavailable initially
	assertOfferingAvailable(t, u, testSKU, "westus", karpv1.CapacityTypeSpot, "Offering should not be marked as unavailable initially")

	// mark the offering as unavailable
	u.MarkUnavailableWithTTL(context.TODO(), "test reason", "NV16as_v4", "westus", karpv1.CapacityTypeSpot, time.Second)

	// test that the offering is now marked as unavailable
	assertOfferingUnavailable(t, u, testSKU, "westus", karpv1.CapacityTypeSpot, "Offering should be marked as unavailable after being marked as such")
	// test that offering is available for different capacity type
	assertOfferingAvailable(t, u, testSKU, "westus", karpv1.CapacityTypeOnDemand, "Offering should be available for different capacity type")

	// wait for the cache entry to expire
	time.Sleep(time.Second)

	// test that the offering is no longer marked as unavailable
	assertOfferingAvailable(t, u, testSKU, "westus", karpv1.CapacityTypeSpot, "Offering should not be marked as unavailable after cache entry has expired")
}

func TestUnavailableOfferingsVMFamilyLevel(t *testing.T) {
	// create a new cache with a short TTL
	singleInstanceCache := cache.New(time.Second, time.Second)
	vmFamilyCache := cache.New(time.Second, time.Second)
	u := NewUnavailableOfferingsWithCache(singleInstanceCache, vmFamilyCache)

	skus := []*skewer.SKU{
		createTestSKU("NV8as_v4", "NVasv4Family", 8),
		createTestSKU("NV16as_v4", "NVasv4Family", 16),
		createTestSKU("NV24as_v4", "NVasv4Family", 24),
	}

	// Test that offerings are not marked as unavailable initially
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable initially")
	}

	// Mark part of the VM family as unavailable (>= 16 CPUs)
	u.MarkFamilyUnavailableWithTTL(context.TODO(), skus[0].GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, 16, time.Second)

	// Test that 16+ CPU offerings are marked as unavailable, but 8 CPU is not
	assertOfferingAvailable(t, u, skus[0], "westus-1", karpv1.CapacityTypeOnDemand, "8 CPU offering should remain available after partial family marking")
	assertOfferingUnavailable(t, u, skus[1], "westus-1", karpv1.CapacityTypeOnDemand, "16 CPU offering should be unavailable after partial family marking")
	assertOfferingUnavailable(t, u, skus[2], "westus-1", karpv1.CapacityTypeOnDemand, "24 CPU offering should be unavailable after partial family marking")

	// Wait for cache expiration
	time.Sleep(time.Second)

	// Test that offerings are no longer marked as unavailable after expiration
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should not be marked as unavailable after cache expiration")
	}

	// Mark the entire VM family as unavailable
	u.MarkFamilyUnavailableWithTTL(context.TODO(), skus[0].GetFamilyName(), "westus-1", karpv1.CapacityTypeOnDemand, -1, time.Second)

	// Test that offerings with both more and fewer than 16 CPUs are now marked as unavailable
	for _, sku := range skus {
		assertOfferingUnavailable(t, u, sku, "westus-1", karpv1.CapacityTypeOnDemand, "Offering should be marked as unavailable after entire family marking")
	}

	// Test that offering from same VM family and version but in a different zone are available
	for _, sku := range skus {
		assertOfferingAvailable(t, u, sku, "westus-2", karpv1.CapacityTypeOnDemand, "Offering in a different zone should be available")
	}

	// Test that offerings from same VM family but different version are available
	differentVersionSKU := createTestSKU("NV16as_v5", "NVasv5Family", 16)
	assertOfferingAvailable(t, u, differentVersionSKU, "westus-1", karpv1.CapacityTypeOnDemand, "Offering from a different version of the same VM family should be available")

	// Wait for cache expiration
	time.Sleep(time.Second)

	// // Test that offerings with both more and fewer than 16 CPUs are now marked as available after expiration
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
