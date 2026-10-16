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

package pricing_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/karpenter-provider-azure/pkg/auth"
	"github.com/Azure/karpenter-provider-azure/pkg/fake"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instancetype"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing/client"
	"k8s.io/apimachinery/pkg/util/sets"
)

var mainCtx context.Context
var ctx context.Context
var stop context.CancelFunc

var fakePricingAPI *fake.PricingAPI
var env *auth.Environment
var providers []*pricing.Provider

func TestAzure(t *testing.T) {
	mainCtx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Providers/Pricing/Azure")
}

var _ = BeforeSuite(func() {
	fakePricingAPI = &fake.PricingAPI{}
	var err error
	env, err = auth.EnvironmentFromName("AzurePublicCloud")
	Expect(err).ToNot(HaveOccurred())
})

var _ = BeforeEach(func() {
	// Create and use ctx in the tests below rather than mainCtx because some of
	// the tests start the pricing poller. Stopping the poller requires canceling the context
	// but if we cancel the main context, it will break tests that run afterwards.
	// We still need the mainCtx because it attaches the test logger which we cannot do
	// in BeforeEach.
	ctx, stop = context.WithCancel(mainCtx)
	providers = []*pricing.Provider{}
	fakePricingAPI.Reset()
})

var _ = AfterEach(func() {
	stop()
	// wait for providers to actually stop
	for _, p := range providers {
		Expect(p.WaitUntilDone()).To(Succeed())
	}
})

var _ = Describe("Pricing", func() {
	It("should return static on-demand data if pricing API fails", func() {
		fakePricingAPI.NextError.Set(fmt.Errorf("failed"))
		p := pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{}))
		providers = append(providers, p)
		price, ok := p.OnDemandPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically(">", 0))
	})
	It("should update on-demand pricing with response from the pricing API", func() {
		// modify our API before creating the pricing provider as it performs an initial update on creation.
		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{
				fake.NewProductPrice("Standard_D1", 1.20),
				fake.NewProductPrice("Standard_D14", 1.23),
			},
		})
		updateStart := time.Now()
		p := pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{}))
		providers = append(providers, p)
		Eventually(func() bool { return p.OnDemandLastUpdated().After(updateStart) }).Should(BeTrue())

		price, ok := p.OnDemandPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.20))

		price, ok = p.OnDemandPrice("Standard_D14", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.23))

		price, ok = p.OnDemandPrice("Standard_D1", pricing.BillingProductWindowsIncluded)
		Expect(ok).To(BeFalse())
		Expect(price).To(Equal(pricing.MissingPrice))
	})

	It("should increment its sequence only when pricing changes", func() {
		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{fake.NewProductPrice("Standard_D1", 1.20)},
		})
		updateStart := time.Now()
		p := pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{}))
		providers = append(providers, p)
		Eventually(func() bool { return p.OnDemandLastUpdated().After(updateStart) }).Should(BeTrue())

		initialSequence := p.SeqNum()
		p.Update(ctx)
		Expect(p.SeqNum()).To(Equal(initialSequence))

		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{fake.NewProductPrice("Standard_D1", 1.30)},
		})
		p.Update(ctx)
		Expect(p.SeqNum()).To(Equal(initialSequence + 1))
		price, known := p.OnDemandPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(known).To(BeTrue())
		Expect(price).To(Equal(1.30))
	})

	It("should preserve base and Windows prices while excluding irrelevant products", func() {
		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{
				{
					ArmSkuName:  "Standard_D2s_v5",
					ProductName: "Virtual Machines Dsv5 Series",
					RetailPrice: 0.096,
				},
				{
					ArmSkuName:  "Standard_D2s_v5",
					ProductName: "Virtual Machines Dsv5 Series Windows",
					RetailPrice: 0.188,
				},
				{
					ArmSkuName:  "Standard_D2s_v5",
					ProductName: "Virtual Machines Dsv5 Series",
					SkuName:     "D2s v5 Spot",
					RetailPrice: 0.020266,
				},
				{
					ArmSkuName:  "Standard_D2s_v5",
					ProductName: "Virtual Machines Dsv5 Series Windows",
					SkuName:     "D2s v5 Spot",
					RetailPrice: 0.039687,
				},
				{
					ArmSkuName:  "Standard_NCC40ads_H100_v5",
					ProductName: "Virtual Machines NCCadsv5 Srs",
					RetailPrice: 7.890,
				},
				{
					ArmSkuName:  "Standard_NCC40ads_H100_v5",
					ProductName: "Virtual Machines NCCadsv5 Srs Win",
					RetailPrice: 9.730,
				},
				{
					ArmSkuName:  "Standard_DC4as_v7",
					ProductName: "DCasv7 Series VM",
					MeterName:   "DC4asv7",
					SkuName:     "DC4asv7",
					RetailPrice: 0.250,
				},
				{
					ArmSkuName:  "Standard_DC4as_v7",
					ProductName: "DCasv7 Series Windows VM",
					MeterName:   "DC4asv7",
					SkuName:     "DC4asv7",
					RetailPrice: 0.434,
				},
				{
					ArmSkuName:  "Standard_D2as_v5",
					ProductName: "Dasv5 Series Cloud Services",
					RetailPrice: 0.200,
				},
				{
					ArmSkuName:  "Standard_E2as_v5",
					ProductName: "Easv5 Series CloudServices",
					RetailPrice: 0.228,
				},
				{
					ArmSkuName:  "Standard_D2s_v5",
					ProductName: "Virtual Machines Dsv5 Series",
					MeterName:   "D2s v5 Low Priority",
					RetailPrice: 0.01,
				},
				{
					ArmSkuName:       "Standard_Expired",
					ProductName:      "Virtual Machines Expired Series",
					RetailPrice:      1.000,
					EffectiveEndDate: time.Now().Add(-time.Hour),
				},
				{
					ArmSkuName:       "Standard_Current",
					ProductName:      "Virtual Machines Current Series",
					RetailPrice:      2.000,
					EffectiveEndDate: time.Now().Add(time.Hour),
				},
			},
		})

		onDemandPrices, spotPrices, err := pricing.FetchPricing(ctx, fakePricingAPI, fake.Region)
		Expect(err).ToNot(HaveOccurred())
		Expect(onDemandPrices.For(pricing.BillingProductBaseCompute)).To(Equal(map[string]float64{
			"Standard_D2s_v5":           0.096,
			"Standard_NCC40ads_H100_v5": 7.890,
			"Standard_DC4as_v7":         0.250,
			"Standard_Current":          2.000,
		}))
		Expect(onDemandPrices.For(pricing.BillingProductWindowsIncluded)).To(Equal(map[string]float64{
			"Standard_D2s_v5":           0.188,
			"Standard_NCC40ads_H100_v5": 9.730,
			"Standard_DC4as_v7":         0.434,
		}))
		Expect(spotPrices.For(pricing.BillingProductBaseCompute)).To(Equal(map[string]float64{
			"Standard_D2s_v5": 0.020266,
		}))
		Expect(spotPrices.For(pricing.BillingProductWindowsIncluded)).To(Equal(map[string]float64{
			"Standard_D2s_v5": 0.039687,
		}))
	})

	DescribeTable("should resolve duplicate prices deterministically",
		func(items []client.Item, expectedPrice float64, expectError bool) {
			fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{Items: items})

			onDemandPrices, _, err := pricing.FetchPricing(ctx, fakePricingAPI, fake.Region)
			if expectError {
				Expect(err).To(MatchError(And(
					ContainSubstring("Standard_D1"),
					ContainSubstring("on-demand"),
					ContainSubstring(string(pricing.BillingProductBaseCompute)),
					ContainSubstring("1.2"),
					ContainSubstring("1.3"),
				)))
				return
			}
			Expect(err).ToNot(HaveOccurred())
			Expect(onDemandPrices.For(pricing.BillingProductBaseCompute)).To(HaveKeyWithValue("Standard_D1", expectedPrice))
		},
		Entry("deduplicates equal prices", []client.Item{
			{ArmSkuName: "Standard_D1", ProductName: "Virtual Machines D Series", MeterID: "meter-a", RetailPrice: 1.20},
			{ArmSkuName: "Standard_D1", ProductName: "Virtual Machines D Series", MeterID: "meter-b", RetailPrice: 1.20},
		}, 1.20, false),
		Entry("selects the sole primary meter", []client.Item{
			{ArmSkuName: "Standard_D1", ProductName: "Virtual Machines D Series", MeterID: "meter-a", RetailPrice: 1.20, IsPrimaryMeterRegion: true},
			{ArmSkuName: "Standard_D1", ProductName: "Virtual Machines D Series", MeterID: "meter-b", RetailPrice: 1.30},
		}, 1.20, false),
		Entry("rejects unresolved values", []client.Item{
			{ArmSkuName: "Standard_D1", ProductName: "Virtual Machines D Series", MeterID: "meter-a", RetailPrice: 1.20},
			{ArmSkuName: "Standard_D1", ProductName: "Virtual Machines D Series", MeterID: "meter-b", RetailPrice: 1.30},
		}, 0.0, true),
	)

	It("should reject an unrecognized Windows product name", func() {
		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{{
				ArmSkuName:  "Standard_D1",
				ProductName: "Virtual Machines Windows D Series",
				RetailPrice: 1.20,
			}},
		})

		_, _, err := pricing.FetchPricing(ctx, fakePricingAPI, fake.Region)
		Expect(err).To(MatchError(ContainSubstring(`unrecognized Windows pricing product "Virtual Machines Windows D Series"`)))
	})

	It("should update spot pricing with response from the pricing API", func() {
		// modify our API before creating the pricing provider as it performs an initial update on creation.
		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{
				fake.NewSpotProductPrice("Standard_D1", 1.10),
				fake.NewSpotProductPrice("Standard_D14", 1.13),
			},
		})
		updateStart := time.Now()
		p := pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{}))
		providers = append(providers, p)
		Eventually(func() bool { return p.SpotLastUpdated().After(updateStart) }).Should(BeTrue())

		price, ok := p.SpotPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.10))

		price, ok = p.SpotPrice("Standard_D14", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.13))
	})

	It("each supported instance type should have pricing at least somewhere", func() {
		regions := pricing.Regions()
		skus := instancetype.GetKarpenterWorkingSKUs()
		for _, region := range regions {
			providers = append(providers, pricing.NewProvider(ctx, env, fakePricingAPI, region, make(chan struct{})))
		}
		// These SKUs are not in the Azure Retail Pricing API and never have been historically.
		// We used to not be asserting in this test so grandfathering these sizes in
		skusWithNoPricingExpected := sets.New(
			"Standard_EC128ieds_v5",
			"Standard_EC128ies_v5",
			"Standard_ND128isr_GB300_v6",
			"Standard_NG32adms_V620_v1",
		)
		var skusWithoutPricing []string
		for _, sku := range skus {
			if skusWithNoPricingExpected.Has(*sku.Name) {
				continue
			}
			foundPricingForSKU := false
			for _, provider := range providers {
				if price, ok := provider.OnDemandPrice(*sku.Name, pricing.BillingProductBaseCompute); ok && price > 0 {
					foundPricingForSKU = true
					break
				}
				if price, ok := provider.SpotPrice(*sku.Name, pricing.BillingProductBaseCompute); ok && price > 0 {
					foundPricingForSKU = true
					break
				}
			}
			if !foundPricingForSKU {
				skusWithoutPricing = append(skusWithoutPricing, *sku.Name)
			}
		}
		if len(skusWithoutPricing) > 0 {
			fmt.Println("\nSKUs without pricing:")
			for _, sku := range skusWithoutPricing {
				fmt.Println(sku)
			}
		}
		Expect(skusWithoutPricing).To(BeEmpty())
	})

	It("should poll pricing data in public clouds", func() {
		// modify our API before creating the pricing provider as it performs an initial update on creation.
		fakePricingAPI.ProductsPricePage.Set(&client.ProductsPricePage{
			Items: []client.Item{
				fake.NewProductPrice("Standard_D1", 1.20),
				fake.NewProductPrice("Standard_D14", 1.23),
				fake.NewSpotProductPrice("Standard_D1", 1.10),
				fake.NewSpotProductPrice("Standard_D14", 1.13),
			},
		})
		start := make(chan struct{}, 1)
		p := pricing.NewProvider(ctx, env, fakePricingAPI, "", start)
		providers = append(providers, p)
		start <- struct{}{}

		Eventually(func(g Gomega) {
			g.Expect(p.OnDemandLastUpdated()).ToNot(Equal(pricing.InitialPriceUpdate))
			g.Expect(p.SpotLastUpdated()).ToNot(Equal(pricing.InitialPriceUpdate))
		}, 3*time.Second).Should(Succeed())

		// Price APIs still work
		price, ok := p.OnDemandPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically(">", 0))

		price, ok = p.SpotPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.1))
	})

	It("should not poll pricing data in non-public clouds", func() {
		fakePricingAPI.NextError.Set(fmt.Errorf("failed"))
		govEnv := &auth.Environment{
			Cloud: cloud.AzureGovernment,
		}
		start := make(chan struct{}, 1)
		p := pricing.NewProvider(ctx, govEnv, fakePricingAPI, "", start)
		providers = append(providers, p)
		start <- struct{}{}

		Consistently(func(g Gomega) {
			g.Expect(p.OnDemandLastUpdated()).To(Equal(pricing.InitialPriceUpdate))
			g.Expect(p.SpotLastUpdated()).To(Equal(pricing.InitialPriceUpdate))
		}, 3*time.Second).Should(Succeed())

		// Price APIs still work
		price, ok := p.OnDemandPrice("Standard_D1", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically(">", 0))
	})

	It("should return MissingPrice for unknown SKUs", func() {
		p := pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{}))
		providers = append(providers, p)

		// On-demand: unknown SKU returns MissingPrice with ok=false
		price, ok := p.OnDemandPrice("Standard_NonExistent_SKU", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeFalse(), "pricing should not be known for a non-existent SKU")
		Expect(price).To(Equal(pricing.MissingPrice), "unknown SKU should get MissingPrice")

		// Spot: unknown SKU returns MissingPrice with ok=false
		price, ok = p.SpotPrice("Standard_NonExistent_SKU", pricing.BillingProductBaseCompute)
		Expect(ok).To(BeFalse(), "spot pricing should not be known for a non-existent SKU")
		Expect(price).To(Equal(pricing.MissingPrice), "unknown SKU should get MissingPrice for spot")
	})
})
