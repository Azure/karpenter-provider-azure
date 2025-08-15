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

package zone_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/zone"
)

func TestProvider_SupportsZones_ZonalRegions(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := context.Background()

	// Setup fake API
	fakeAPI := &fake.SubscriptionsAPI{}
	fakeAPI.Locations.Store("eastus", createZonalLocation("eastus", []string{"1"}))
	fakeAPI.Locations.Store("centralus", createZonalLocation("centralus", []string{"1", "2"}))

	// Create provider
	provider := zone.NewProvider(fakeAPI, "test-subscription")

	// Test regions
	g.Expect(provider.SupportsZones(ctx, "eastus")).To(BeTrue())
	g.Expect(provider.SupportsZones(ctx, "centralus")).To(BeTrue())

	// Test unknown region
	g.Expect(provider.SupportsZones(ctx, "unknownregion")).To(BeFalse())
}

func TestProvider_SupportsZones_NonZonalRegions(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := context.Background()

	// Setup fake API
	fakeAPI := &fake.SubscriptionsAPI{}
	fakeAPI.Locations.Store("westus", createNonZonalLocation("westus"))

	// Create provider
	provider := zone.NewProvider(fakeAPI, "test-subscription")

	// Test regions
	g.Expect(provider.SupportsZones(ctx, "westus")).To(BeFalse())

	// Test unknown region
	g.Expect(provider.SupportsZones(ctx, "unknownregion")).To(BeFalse())
}

func TestProvider_SupportsZones_FallbackToHardcodedListOnError(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := context.Background()

	// Setup fake API with error
	fakeAPI := &fake.SubscriptionsAPI{}
	fakeAPI.NewListLocationsPagerBehavior.Error.Set(errors.New("API error"))

	// Create provider
	provider := zone.NewProvider(fakeAPI, "test-subscription")

	// Test regions that are in the hardcoded fallback list
	g.Expect(provider.SupportsZones(ctx, "eastus")).To(BeTrue())
	g.Expect(provider.SupportsZones(ctx, "westus2")).To(BeTrue())
	g.Expect(provider.SupportsZones(ctx, "northeurope")).To(BeTrue())

	// Test region not in hardcoded list
	g.Expect(provider.SupportsZones(ctx, "unknownregion")).To(BeFalse())
}

func TestProvider_SupportsZones_StopsLoadingAfterMaxFailures(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := context.Background()

	// Setup fake API with error
	fakeAPI := &fake.SubscriptionsAPI{}
	fakeAPI.NewListLocationsPagerBehavior.Error.Set(errors.New("API error"), fake.MaxCalls(50))

	// Create provider
	provider := zone.NewProvider(fakeAPI, "test-subscription")

	// Test regions that are in the hardcoded fallback list
	for i := 0; i < 20; i++ {
		g.Expect(provider.SupportsZones(ctx, "eastus")).To(BeTrue())
	}

	g.Expect(fakeAPI.NewListLocationsPagerBehavior.FailedCalls()).To(Equal(10))
}

func TestProvider_SupportsZones_Caching(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := context.Background()

	// Setup fake API
	fakeAPI := &fake.SubscriptionsAPI{}
	fakeAPI.Locations.Store("eastus", createZonalLocation("eastus", []string{"1"}))
	fakeAPI.Locations.Store("westus2", createZonalLocation("westus2", []string{"1"}))
	fakeAPI.Locations.Store("northeurope", createZonalLocation("northeurope", []string{"1"}))

	// Create provider
	provider := zone.NewProvider(fakeAPI, "test-subscription")

	// First call should trigger API call
	g.Expect(provider.SupportsZones(ctx, "eastus")).To(BeTrue())
	g.Expect(fakeAPI.NewListLocationsPagerBehavior.Calls()).To(Equal(1))

	// Second call should use cached data
	g.Expect(provider.SupportsZones(ctx, "eastus")).To(BeTrue())
	g.Expect(fakeAPI.NewListLocationsPagerBehavior.Calls()).To(Equal(1))

	// Third call with different region should also use cached data
	g.Expect(provider.SupportsZones(ctx, "unknownregion")).To(BeFalse())
	g.Expect(fakeAPI.NewListLocationsPagerBehavior.Calls()).To(Equal(1))
}

func TestProvider_SupportsZones_ThreadSafety(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	ctx := context.Background()

	// Setup fake API
	fakeAPI := &fake.SubscriptionsAPI{}

	// Add mock location using helper function
	fakeAPI.Locations.Store("eastus", createZonalLocation("eastus", []string{"1"}))

	provider := zone.NewProvider(fakeAPI, "test-subscription")

	// Test concurrent access
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			g.Expect(provider.SupportsZones(ctx, "eastus")).To(BeTrue())
			g.Expect(provider.SupportsZones(ctx, "unknownregion")).To(BeFalse())
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// createZonalLocation creates a location with availability zone mappings
func createZonalLocation(name string, zones []string) armsubscriptions.Location {
	var zoneMappings []*armsubscriptions.AvailabilityZoneMappings
	for _, zone := range zones {
		zoneMappings = append(zoneMappings, &armsubscriptions.AvailabilityZoneMappings{
			LogicalZone:  lo.ToPtr(zone),
			PhysicalZone: lo.ToPtr(name + "-az" + zone),
		})
	}

	return armsubscriptions.Location{
		Name:                     lo.ToPtr(name),
		AvailabilityZoneMappings: zoneMappings,
	}
}

// createNonZonalLocation creates a location without availability zone mappings
func createNonZonalLocation(name string) armsubscriptions.Location {
	return armsubscriptions.Location{
		Name: lo.ToPtr(name),
		// No AvailabilityZoneMappings for non-zonal regions
	}
}
