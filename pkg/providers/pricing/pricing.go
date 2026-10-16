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
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

// MissingPrice is a high penalty price assigned to SKUs with no known pricing data.
// Setting to a high value means it won't be chosen automatically,
// but will still be available if chosen explicitly.
const MissingPrice float64 = 999.0

type BillingProduct string

const (
	BillingProductBaseCompute     BillingProduct = "base-compute"
	BillingProductWindowsIncluded BillingProduct = "windows-included"
)

type Prices map[BillingProduct]map[string]float64

func newPrices() Prices {
	return Prices{
		BillingProductBaseCompute:     {},
		BillingProductWindowsIncluded: {},
	}
}

func (p Prices) For(product BillingProduct) map[string]float64 {
	return p[product]
}

func (p Prices) Len() int {
	return len(p[BillingProductBaseCompute]) + len(p[BillingProductWindowsIncluded])
}

func (p Prices) InstanceTypes() []string {
	return lo.Union(lo.Keys(p[BillingProductBaseCompute]), lo.Keys(p[BillingProductWindowsIncluded]))
}

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

	mu                 sync.RWMutex
	onDemandUpdateTime time.Time
	onDemandPrices     Prices
	spotUpdateTime     time.Time
	spotPrices         Prices
	seqNum             atomic.Uint64
	done               chan struct{}
}

// NewPricingAPI returns a pricing API
func NewAPI(cloud cloud.Configuration) client.PricingAPI {
	return client.New(cloud)
}

func initialPrices(region string) Prices {
	basePrices, ok := initialBaseOnDemandPrices[region]
	if !ok {
		basePrices = initialBaseOnDemandPrices[defaultRegion]
	}
	windowsPrices, ok := initialWindowsOnDemandPrices[region]
	if !ok {
		windowsPrices = initialWindowsOnDemandPrices[defaultRegion]
	}
	return Prices{
		BillingProductBaseCompute:     basePrices,
		BillingProductWindowsIncluded: windowsPrices,
	}
}

func NewProvider(
	ctx context.Context,
	env *auth.Environment,
	pricing client.PricingAPI,
	region string,
	startAsync <-chan struct{},
) *Provider {
	staticPricing := initialPrices(region)

	p := &Provider{
		region:             region,
		onDemandUpdateTime: InitialPriceUpdate,
		onDemandPrices:     staticPricing,
		spotUpdateTime:     InitialPriceUpdate,
		// default our spot pricing to the same as the on-demand pricing until a price update
		spotPrices: staticPricing,
		pricing:    pricing,
		cm:         pretty.NewChangeMonitor(),
		done:       make(chan struct{}),
	}
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithName("pricing").WithValues("region", region))

	// Only poll in public cloud. Other clouds aren't supported currently
	if auth.IsPublic(env.Cloud) {
		go func() {
			log.FromContext(ctx).V(0).Info("starting pricing update loop")
			// perform an initial price update at startup
			p.Update(ctx)

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
				p.Update(ctx)
			}

			for {
				select {
				case <-ctx.Done():
					close(p.done)
					return
				case <-time.After(pricingUpdatePeriod):
					p.Update(ctx)
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
	return lo.Union(p.onDemandPrices.InstanceTypes(), p.spotPrices.InstanceTypes())
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

// OnDemandPrice returns the last known on-demand price for a given instance type and billing product.
// The boolean return indicates whether a real price was found (true) or the penalty
// MissingPrice is being returned (false).
func (p *Provider) OnDemandPrice(instanceType string, product BillingProduct) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	price, ok := p.onDemandPrices.For(product)[instanceType]
	if !ok {
		return MissingPrice, false
	}
	return price, true
}

// SpotPrice returns the last known spot price for a given instance type and billing product.
// The boolean return indicates whether a real price was found (true) or the penalty
// MissingPrice is being returned (false).
func (p *Provider) SpotPrice(instanceType string, product BillingProduct) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	price, ok := p.spotPrices.For(product)[instanceType]
	if !ok {
		return MissingPrice, false
	}
	return price, true
}

// SeqNum returns a monotonically increasing counter for material pricing changes.
func (p *Provider) SeqNum() uint64 {
	return p.seqNum.Load()
}

// Update fetches and atomically applies the latest pricing snapshot.
func (p *Provider) Update(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	onDemandPrices, spotPrices, err := FetchPricing(ctx, p.pricing, p.region)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.FromContext(ctx).Error(err, "failed to fetch updated pricing, using existing pricing data")
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	changed := false
	if onDemandPrices.Len() > 0 {
		onDemandChanged := p.cm.HasChanged("on-demand-prices", onDemandPrices)
		p.onDemandPrices = onDemandPrices
		p.onDemandUpdateTime = time.Now()
		if onDemandChanged {
			changed = true
			log.FromContext(ctx).Info("updated on-demand pricing",
				"instanceTypeCount", p.onDemandPrices.Len(),
			)
		}
	} else {
		log.FromContext(ctx).Error(errors.New("no on-demand pricing found"), "using existing on-demand pricing data")
	}

	if spotPrices.Len() > 0 {
		spotChanged := p.cm.HasChanged("spot-prices", spotPrices)
		p.spotPrices = spotPrices
		p.spotUpdateTime = time.Now()
		if spotChanged {
			changed = true
			log.FromContext(ctx).Info("updated spot pricing",
				"instanceTypeCount", p.spotPrices.Len(),
			)
		}
	} else {
		log.FromContext(ctx).Error(errors.New("no spot pricing found"), "using existing spot pricing data")
	}
	if changed {
		p.seqNum.Add(1)
	}
}

// FetchPricing fetches VM pricing from the Azure retail pricing API for the given region,
// returning on-demand and spot prices keyed by billing product and ARM SKU name.
func FetchPricing(ctx context.Context, pricingAPI client.PricingAPI, region string) (onDemandPrices, spotPrices Prices, err error) {
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
			Value:    region,
		}}

	prices := map[client.Item]bool{}
	if err := pricingAPI.GetProductsPricePages(ctx, filters, processPage(prices, time.Now())); err != nil {
		return nil, nil, err
	}

	return categorizePrices(prices)
}

func processPage(prices map[client.Item]bool, fetchTime time.Time) func(page *client.ProductsPricePage) {
	return func(page *client.ProductsPricePage) {
		for _, pItem := range page.Items {
			if !pItem.EffectiveEndDate.IsZero() && pItem.EffectiveEndDate.Before(fetchTime) {
				continue
			}
			if strings.HasSuffix(pItem.MeterName, " Low Priority") {
				// https://learn.microsoft.com/en-us/azure/batch/batch-spot-vms#differences-between-spot-and-low-priority-vms
				continue
			}
			// Some advertised prices have a different Cloud Services and Virtual Machines price. The only way
			// to differentiate seems to be by looking at the product name suffix. We want the VM price not the cloud services price.
			if strings.HasSuffix(pItem.ProductName, " Cloud Services") ||
				strings.HasSuffix(pItem.ProductName, " CloudServices") {
				continue
			}
			prices[pItem] = true
		}
	}
}

func billingProductForProductName(productName string) (BillingProduct, error) {
	switch {
	case strings.HasSuffix(productName, " Windows"),
		strings.HasSuffix(productName, " Win"),
		strings.HasSuffix(productName, " Windows VM"):
		return BillingProductWindowsIncluded, nil
	case strings.Contains(productName, "Windows"):
		return "", fmt.Errorf("unrecognized Windows pricing product %q", productName)
	default:
		return BillingProductBaseCompute, nil
	}
}

type priceKey struct {
	instanceType   string
	capacityType   string
	billingProduct BillingProduct
}

func selectPrice(key priceKey, candidates []client.Item) (float64, error) {
	prices := map[float64]struct{}{}
	var primary []client.Item
	for _, candidate := range candidates {
		prices[candidate.RetailPrice] = struct{}{}
		if candidate.IsPrimaryMeterRegion {
			primary = append(primary, candidate)
		}
	}
	if len(prices) == 1 {
		for price := range prices {
			return price, nil
		}
	}
	if len(primary) == 1 {
		return primary[0].RetailPrice, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RetailPrice != candidates[j].RetailPrice {
			return candidates[i].RetailPrice < candidates[j].RetailPrice
		}
		return candidates[i].ProductName < candidates[j].ProductName
	})
	return 0, fmt.Errorf(
		"conflicting prices for instance type %q, capacity type %q, billing product %q: %v",
		key.instanceType,
		key.capacityType,
		key.billingProduct,
		candidates,
	)
}

func categorizePrices(prices map[client.Item]bool) (Prices, Prices, error) {
	grouped := map[priceKey][]client.Item{}
	for price := range prices {
		product, err := billingProductForProductName(price.ProductName)
		if err != nil {
			return nil, nil, err
		}
		capacityType := "on-demand"
		if strings.HasSuffix(price.SkuName, " Spot") {
			capacityType = "spot"
		}
		key := priceKey{
			instanceType:   price.ArmSkuName,
			capacityType:   capacityType,
			billingProduct: product,
		}
		grouped[key] = append(grouped[key], price)
	}

	onDemandPrices := newPrices()
	spotPrices := newPrices()
	for key, candidates := range grouped {
		price, err := selectPrice(key, candidates)
		if err != nil {
			return nil, nil, err
		}
		targetPrices := onDemandPrices.For(key.billingProduct)
		if key.capacityType == "spot" {
			targetPrices = spotPrices.For(key.billingProduct)
		}
		targetPrices[key.instanceType] = price
	}
	return onDemandPrices, spotPrices, nil
}

func (p *Provider) LivenessProbe(_ *http.Request) error {
	// ensure we don't deadlock and nolint for the empty critical section
	p.mu.Lock()
	//nolint: staticcheck
	p.mu.Unlock()
	return nil
}

func (p *Provider) Reset() {
	staticPricing := initialPrices(p.region)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.onDemandPrices = staticPricing
	p.onDemandUpdateTime = InitialPriceUpdate
	p.spotPrices = staticPricing
	p.spotUpdateTime = InitialPriceUpdate
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
	return lo.Keys(initialBaseOnDemandPrices)
}
