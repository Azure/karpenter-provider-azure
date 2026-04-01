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

package pricing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/utils/pretty"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing/client"
)

// pricingUpdatePeriod is how often we try to update our pricing information after the initial update on startup
const pricingUpdatePeriod = 12 * time.Hour

const defaultRegion = "eastus"

// Provider provides actual pricing data to the Azure cloud provider to allow it to make more informed decisions
// regarding which instances to launch.  This is initialized at startup with a periodically updated static price list to
// support running in locations where pricing data is unavailable.  In those cases the static pricing data provides a
// relative ordering that is still more accurate than our previous pricing model.  In the event that a pricing update
// fails, the previous pricing information is retained and used which may be the static initial pricing data if pricing
// updates never succeed.
type Provider struct {
	pricing client.PricingAPI
	region  string
	cm      *pretty.ChangeMonitor

	mu                       sync.RWMutex
	onDemandUpdateTime       time.Time
	onDemandPrices           map[string]float64
	spotUpdateTime           time.Time
	spotPrices               map[string]float64
	savingsPlanUpdateTime    time.Time
	savingsPlanPrices        map[string]float64
	done                     chan struct{}
}

type Err struct {
	error
	lastOnDemandUpdateTime    time.Time
	lastSpotUpdateTime        time.Time
	lastSavingsPlanUpdateTime time.Time
}

// NewPricingAPI returns a pricing API
func NewAPI(cloud cloud.Configuration) client.PricingAPI {
	return client.New(cloud)
}

func NewProvider(
	ctx context.Context,
	env *auth.Environment,
	pricing client.PricingAPI,
	region string,
	startAsync <-chan struct{},
) *Provider {
	// see if we've got region specific pricing data
	staticPricing, ok := initialOnDemandPrices[region]
	if !ok {
		// and if not, fall back to the always available eastus
		staticPricing = initialOnDemandPrices[defaultRegion]
	}

	p := &Provider{
		region:                region,
		onDemandUpdateTime:    initialPriceUpdate,
		onDemandPrices:        staticPricing,
		spotUpdateTime:        initialPriceUpdate,
		// default our spot pricing to the same as the on-demand pricing until a price update
		spotPrices:            staticPricing,
		savingsPlanUpdateTime: initialPriceUpdate,
		savingsPlanPrices:     map[string]float64{},
		pricing:               pricing,
		cm:                    pretty.NewChangeMonitor(),
		done:                  make(chan struct{}),
	}
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName("pricing").WithValues("region", region))

	// Only poll in public cloud. Other clouds aren't supported currently
	if auth.IsPublic(env.Cloud) {
		go func() {
			log.FromContext(ctx).V(0).Info("starting pricing update loop")
			// perform an initial price update at startup
			p.updatePricing(ctx)

			startup := time.Now()
			// wait for leader election or to be signaled to exit
			select {
			case <-startAsync:
			case <-ctx.Done():
				close(p.done)
				return
			}
			// if it took many hours to be elected leader, we want to re-fetch pricing before we start our periodic
			// polling
			if time.Since(startup) > pricingUpdatePeriod {
				p.updatePricing(ctx)
			}

			for {
				select {
				case <-ctx.Done():
					close(p.done)
					return
				case <-time.After(pricingUpdatePeriod):
					p.updatePricing(ctx)
				}
			}
		}()
	} else {
		close(p.done) // done immediately
	}
	return p
}

// InstanceTypes returns the list of all instance types for which either a price is known.
func (p *Provider) InstanceTypes() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return lo.Union(lo.Keys(p.onDemandPrices), lo.Keys(p.spotPrices), lo.Keys(p.savingsPlanPrices))
}

// OnDemandLastUpdated returns the time that the on-demand pricing was last updated
func (p *Provider) OnDemandLastUpdated() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.onDemandUpdateTime
}

// SpotLastUpdated returns the time that the spot pricing was last updated
func (p *Provider) SpotLastUpdated() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.spotUpdateTime
}

// SavingsPlanLastUpdated returns the time that the savings plan pricing was last updated
func (p *Provider) SavingsPlanLastUpdated() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.savingsPlanUpdateTime
}

// OnDemandPrice returns the last known on-demand price for a given instance type, returning false if there is no
// known on-demand pricing for the instance type.
func (p *Provider) OnDemandPrice(instanceType string) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	price, ok := p.onDemandPrices[instanceType]
	if !ok {
		// if we don't have a price, check if it's a known SKU with missing price
		if price, ok = skusWithMissingPrice[instanceType]; ok {
			return price, true
		}
		return 0.0, false
	}
	return price, true
}

// SpotPrice returns the last known spot price for a given instance type, returning false
// if there is no known spot pricing for that instance type
func (p *Provider) SpotPrice(instanceType string) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	price, ok := p.spotPrices[instanceType]
	if !ok {
		// if we don't have a price, check if it's a known SKU with missing price
		if price, ok = skusWithMissingPrice[instanceType]; ok {
			return price, true
		}
		return 0.0, false
	}
	return price, true
}

// SavingsPlanPrice returns the best (lowest) known savings plan price for a given instance type,
// returning false if there is no known savings plan pricing for that instance type.
func (p *Provider) SavingsPlanPrice(instanceType string) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	price, ok := p.savingsPlanPrices[instanceType]
	if !ok {
		return 0.0, false
	}
	return price, true
}

func (p *Provider) updatePricing(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	prices := []client.Item{}
	err := p.fetchPricing(ctx, processPage(&prices))
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.FromContext(ctx).Error(err, "failed to fetch updated pricing, using existing pricing data",
			"lastOnDemandUpdateTime", err.lastOnDemandUpdateTime.Format(time.RFC3339),
			"lastSpotUpdateTime", err.lastSpotUpdateTime.Format(time.RFC3339),
			"lastSavingsPlanUpdateTime", err.lastSavingsPlanUpdateTime.Format(time.RFC3339),
		)
		return
	}

	onDemandPrices, spotPrices, savingsPlanPrices := categorizePrices(prices)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.UpdateOnDemandPricing(ctx, onDemandPrices); err != nil {
			log.FromContext(ctx).Error(err, "failed to update on-demand pricing, using existing pricing data",
				"lastOnDemandUpdateTime", err.lastOnDemandUpdateTime.Format(time.RFC3339),
			)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.UpdateSpotPricing(ctx, spotPrices); err != nil {
			log.FromContext(ctx).Error(err, "failed to update spot pricing, using existing pricing data",
				"lastSpotUpdateTime", err.lastSpotUpdateTime.Format(time.RFC3339),
			)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.UpdateSavingsPlanPricing(ctx, savingsPlanPrices); err != nil {
			log.FromContext(ctx).Error(err, "failed to update savings plan pricing, using existing pricing data",
				"lastSavingsPlanUpdateTime", err.lastSavingsPlanUpdateTime.Format(time.RFC3339),
			)
		}
	}()

	wg.Wait()
}

func (p *Provider) UpdateOnDemandPricing(ctx context.Context, onDemandPrices map[string]float64) *Err {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(onDemandPrices) == 0 {
		return &Err{error: errors.New("no on-demand pricing found"), lastOnDemandUpdateTime: p.onDemandUpdateTime}
	}

	p.onDemandPrices = lo.Assign(onDemandPrices)
	p.onDemandUpdateTime = time.Now()
	if p.cm.HasChanged("on-demand-prices", p.onDemandPrices) {
		log.FromContext(ctx).Info("updated on-demand pricing",
			"instanceTypeCount", len(p.onDemandPrices),
		)
	}
	return nil
}

func (p *Provider) fetchPricing(ctx context.Context, pageHandler func(output *client.ProductsPricePage)) *Err {
	p.mu.Lock()
	defer p.mu.Unlock()
	filters := []*client.Filter{
		{
			Field:    "priceType",
			Operator: client.Equals,
			Value:    "Consumption",
		},
		{
			Field:    "currencyCode",
			Operator: client.Equals,
			Value:    "USD",
		},
		{
			Field:    "serviceFamily",
			Operator: client.Equals,
			Value:    "Compute",
		},
		{
			Field:    "serviceName",
			Operator: client.Equals,
			Value:    "Virtual Machines",
		},
		{
			Field:    "armRegionName",
			Operator: client.Equals,
			Value:    p.region,
		}}
	err := p.pricing.GetProductsPricePages(ctx, filters, pageHandler)
	if err != nil {
		return &Err{error: err, lastOnDemandUpdateTime: p.onDemandUpdateTime, lastSpotUpdateTime: p.spotUpdateTime, lastSavingsPlanUpdateTime: p.savingsPlanUpdateTime}
	}
	return nil
}

func processPage(prices *[]client.Item) func(page *client.ProductsPricePage) {
	return func(page *client.ProductsPricePage) {
		for _, pItem := range page.Items {
			if strings.HasSuffix(pItem.ProductName, " Windows") {
				continue
			}
			if strings.HasSuffix(pItem.MeterName, " Low Priority") {
				// https://learn.microsoft.com/en-us/azure/batch/batch-spot-vms#differences-between-spot-and-low-priority-vms
				continue
			}
			*prices = append(*prices, pItem)
		}
	}
}

func (p *Provider) UpdateSpotPricing(ctx context.Context, spotPrices map[string]float64) *Err {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(spotPrices) == 0 {
		return &Err{error: errors.New("no spot pricing found"), lastSpotUpdateTime: p.spotUpdateTime}
	}

	p.spotPrices = lo.Assign(spotPrices)
	p.spotUpdateTime = time.Now()
	if p.cm.HasChanged("spot-prices", p.spotPrices) {
		log.FromContext(ctx).Info("updated spot pricing",
			"instanceTypeCount", len(p.spotPrices),
		)
	}
	return nil
}

func (p *Provider) UpdateSavingsPlanPricing(ctx context.Context, savingsPlanPrices map[string]float64) *Err {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(savingsPlanPrices) == 0 {
		// Savings plan data may legitimately be absent (e.g., some SKUs don't have SP pricing).
		// This is not an error — just skip the update and retain any existing data.
		return nil
	}

	p.savingsPlanPrices = lo.Assign(savingsPlanPrices)
	p.savingsPlanUpdateTime = time.Now()
	if p.cm.HasChanged("savings-plan-prices", p.savingsPlanPrices) {
		log.FromContext(ctx).Info("updated savings plan pricing",
			"instanceTypeCount", len(p.savingsPlanPrices),
		)
	}
	return nil
}

func categorizePrices(prices []client.Item) (map[string]float64, map[string]float64, map[string]float64) {
	var onDemandPrices, spotPrices, savingsPlanPrices = map[string]float64{}, map[string]float64{}, map[string]float64{}
	for _, price := range prices {
		if strings.HasSuffix(price.SkuName, " Spot") {
			spotPrices[price.ArmSkuName] = price.RetailPrice
		} else {
			onDemandPrices[price.ArmSkuName] = price.RetailPrice
			// Savings plan prices are attached to on-demand (non-spot) items.
			// Store the best (lowest) price across all terms for each SKU.
			if len(price.SavingsPlan) > 0 {
				bestPrice := price.SavingsPlan[0].RetailPrice
				for _, sp := range price.SavingsPlan[1:] {
					if sp.RetailPrice < bestPrice {
						bestPrice = sp.RetailPrice
					}
				}
				// Only store if this is the first entry or lower than what we have
				// (handles case where multiple items map to the same ArmSkuName)
				if existing, ok := savingsPlanPrices[price.ArmSkuName]; !ok || bestPrice < existing {
					savingsPlanPrices[price.ArmSkuName] = bestPrice
				}
			}
		}
	}
	return onDemandPrices, spotPrices, savingsPlanPrices
}

func (p *Provider) LivenessProbe(_ *http.Request) error {
	// ensure we don't deadlock and nolint for the empty critical section
	p.mu.Lock()
	//nolint: staticcheck
	p.mu.Unlock()
	return nil
}

func (p *Provider) Reset() {
	// see if we've got region specific pricing data
	staticPricing, ok := initialOnDemandPrices[p.region]
	if !ok {
		// and if not, fall back to the always available eastus
		staticPricing = initialOnDemandPrices[defaultRegion]
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.onDemandPrices = staticPricing
	p.onDemandUpdateTime = initialPriceUpdate
	p.savingsPlanPrices = map[string]float64{}
	p.savingsPlanUpdateTime = initialPriceUpdate
}

// WaitUntilDone should be called after canceling the context passed to NewProvider to wait until all goroutines have exited
func (p *Provider) WaitUntilDone() error {
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("timeout waiting for pricing provider to shut down")
	}
}

func Regions() []string {
	return lo.Keys(initialOnDemandPrices)
}
