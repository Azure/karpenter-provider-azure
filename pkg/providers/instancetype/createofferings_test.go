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

package instancetype

import (
	"context"
	"errors"
	"testing"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	kcache "github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing/client"
	"github.com/Azure/skewer"

	. "github.com/onsi/gomega"
)

// noopPricingAPI is a minimal PricingAPI that always fails (so the provider uses only
// manually injected prices, not fetched ones). This avoids importing the fake package
// which creates an import cycle in internal tests.
type noopPricingAPI struct{}

func (n *noopPricingAPI) GetProductsPricePages(_ context.Context, _ []*client.Filter, _ func(output *client.ProductsPricePage)) error {
	return errors.New("no pricing data (test stub)")
}

// newTestPricingProvider creates a pricing.Provider suitable for unit tests.
// It uses a non-public cloud config so the pricing update goroutine doesn't start
// (avoids the goroutine calling the noop API repeatedly).
func newTestPricingProvider(ctx context.Context) *pricing.Provider {
	// Use a non-public cloud to prevent the async pricing update loop from starting.
	// We'll inject prices manually via UpdateOnDemandPricing / UpdateSavingsPlanPricing.
	azureEnv := lo.Must(auth.EnvironmentFromName("AzurePublicCloud"))
	return pricing.NewProvider(ctx, azureEnv, &noopPricingAPI{}, "testregion", make(chan struct{}))
}

func TestCreateOfferingsWithSavingsPlanPrice(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	pp := newTestPricingProvider(ctx)
	// Inject on-demand prices for two SKUs
	pp.UpdateOnDemandPricing(ctx, map[string]float64{
		"Standard_D2s_v3": 0.50,
		"Standard_D4s_v3": 0.30,
	})

	// Standard_D2s_v3 has a Savings Plan price that is lower than its on-demand price.
	// Standard_D4s_v3 has no SP price — it should keep its retail price.
	pp.UpdateSavingsPlanPricing(map[string]float64{
		"Standard_D2s_v3": 0.20, // SP discount: 0.20 < 0.50 retail
	})

	provider := &DefaultProvider{
		pricingProvider:      pp,
		unavailableOfferings: kcache.NewUnavailableOfferings(),
	}

	// Test SKU with SP price lower than on-demand → effective price should be SP price
	sku1 := &skewer.SKU{Name: lo.ToPtr("Standard_D2s_v3")}
	zones := sets.New("westus-1")
	offerings := provider.createOfferings(sku1, zones)

	g.Expect(offerings).To(HaveLen(2)) // one on-demand, one spot
	for _, o := range offerings {
		if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeOnDemand) {
			// OnDemand price should be the SP price (0.20), not the retail price (0.50)
			g.Expect(o.Price).To(BeNumerically("~", 0.20, 0.001),
				"OnDemand offering price should use Savings Plan price when it's lower than retail")
			g.Expect(o.Available).To(BeTrue())
		}
	}

	// Test SKU without SP price → effective price should be retail price
	sku2 := &skewer.SKU{Name: lo.ToPtr("Standard_D4s_v3")}
	offerings2 := provider.createOfferings(sku2, zones)

	g.Expect(offerings2).To(HaveLen(2))
	for _, o := range offerings2 {
		if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeOnDemand) {
			g.Expect(o.Price).To(BeNumerically("~", 0.30, 0.001),
				"OnDemand offering price should stay at retail when no SP price exists")
			g.Expect(o.Available).To(BeTrue())
		}
	}
}

func TestCreateOfferingsWithSavingsPlanPriceHigherThanRetail(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	pp := newTestPricingProvider(ctx)
	pp.UpdateOnDemandPricing(ctx, map[string]float64{
		"Standard_D2s_v3": 0.30,
	})
	// SP price higher than retail — should be ignored (retail is cheaper)
	pp.UpdateSavingsPlanPricing(map[string]float64{
		"Standard_D2s_v3": 0.50,
	})

	provider := &DefaultProvider{
		pricingProvider:      pp,
		unavailableOfferings: kcache.NewUnavailableOfferings(),
	}

	sku := &skewer.SKU{Name: lo.ToPtr("Standard_D2s_v3")}
	offerings := provider.createOfferings(sku, sets.New("westus-1"))

	for _, o := range offerings {
		if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeOnDemand) {
			g.Expect(o.Price).To(BeNumerically("~", 0.30, 0.001),
				"OnDemand offering price should stay at retail when SP price is higher")
		}
	}
}

func TestCreateOfferingsSpotUnaffectedBySavingsPlan(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	pp := newTestPricingProvider(ctx)
	pp.UpdateOnDemandPricing(ctx, map[string]float64{
		"Standard_D2s_v3": 0.50,
	})
	pp.UpdateSpotPricing(ctx, map[string]float64{
		"Standard_D2s_v3": 0.10,
	})
	pp.UpdateSavingsPlanPricing(map[string]float64{
		"Standard_D2s_v3": 0.20,
	})

	provider := &DefaultProvider{
		pricingProvider:      pp,
		unavailableOfferings: kcache.NewUnavailableOfferings(),
	}

	sku := &skewer.SKU{Name: lo.ToPtr("Standard_D2s_v3")}
	offerings := provider.createOfferings(sku, sets.New("westus-1"))

	for _, o := range offerings {
		if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeSpot) {
			g.Expect(o.Price).To(BeNumerically("~", 0.10, 0.001),
				"Spot offering price should not be affected by Savings Plan pricing")
			g.Expect(o.Available).To(BeTrue())
		}
		if o.Requirements.Get(karpv1.CapacityTypeLabelKey).Has(karpv1.CapacityTypeOnDemand) {
			g.Expect(o.Price).To(BeNumerically("~", 0.20, 0.001),
				"OnDemand offering should use SP price (0.20) over retail (0.50)")
		}
	}
}
