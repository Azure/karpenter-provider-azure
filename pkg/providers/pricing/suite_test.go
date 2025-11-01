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
)

var mainCtx context.Context
var ctx context.Context
var stop context.CancelFunc

var fakePricingAPI *fake.PricingAPI
var env *auth.Environment
var pricingProviders []*pricing.Provider

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

// trackProvider adds a pricing provider to the list of providers to be cleaned up after the test
func trackProvider(p *pricing.Provider) *pricing.Provider {
	pricingProviders = append(pricingProviders, p)
	return p
}

var _ = BeforeEach(func() {
	// Create and use ctx in the tests below rather than mainCtx because some of
	// the tests start the pricing poller. Stopping the poller requires canceling the context
	// but if we cancel the main context, it will break tests that run afterwards.
	// We still need the mainCtx because it attaches the test logger which we cannot do
	// in BeforeEach.
	ctx, stop = context.WithCancel(mainCtx)
	fakePricingAPI.Reset()
	pricingProviders = []*pricing.Provider{}
})

var _ = AfterEach(func() {
	stop()
	// Wait for all pricing provider goroutines to exit
	for _, p := range pricingProviders {
		p.Shutdown()
	}
})

var _ = Describe("Pricing", func() {
	It("should return static on-demand data if pricing API fails", func() {
		fakePricingAPI.NextError.Set(fmt.Errorf("failed"))
		p := trackProvider(pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{})))
		price, ok := p.OnDemandPrice("Standard_D1")
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
		p := trackProvider(pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{})))
		Eventually(func() bool { return p.OnDemandLastUpdated().After(updateStart) }).Should(BeTrue())

		price, ok := p.OnDemandPrice("Standard_D1")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.20))

		price, ok = p.OnDemandPrice("Standard_D14")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.23))
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
		p := trackProvider(pricing.NewProvider(ctx, env, fakePricingAPI, "", make(chan struct{})))
		Eventually(func() bool { return p.SpotLastUpdated().After(updateStart) }).Should(BeTrue())

		price, ok := p.SpotPrice("Standard_D1")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.10))

		price, ok = p.SpotPrice("Standard_D14")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.13))
	})

	It("each supported instance type should have pricing at least somewhere", func() {
		// for now just print the names of the SKUs that don't have pricing
		fmt.Println("\nSKUs that don't have pricing:")

		regions := pricing.Regions()
		skus := instancetype.GetKarpenterWorkingSKUs()
		providers := []*pricing.Provider{}
		for _, region := range regions {
			providers = append(providers, trackProvider(pricing.NewProvider(ctx, env, fakePricingAPI, region, make(chan struct{}))))
		}
		for _, sku := range skus {
			foundPricingForSKU := false
			for _, provider := range providers {
				if price, ok := provider.OnDemandPrice(*sku.Name); ok && price > 0 {
					foundPricingForSKU = true
					break
				}
				if price, ok := provider.SpotPrice(*sku.Name); ok && price > 0 {
					foundPricingForSKU = true
					break
				}
			}
			if !foundPricingForSKU {
				fmt.Printf("%s\n", *sku.Name)
			}
		}
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
		p := trackProvider(pricing.NewProvider(ctx, env, fakePricingAPI, "", start))
		start <- struct{}{}

		// TODO: If this were exported or we were in the same package we could just assert on the package variable rather than
		// duplicating it here
		expectedTime, _ := time.Parse(time.RFC3339, "2025-06-03T21:16:07Z")
		Eventually(func(g Gomega) {
			g.Expect(p.OnDemandLastUpdated()).ToNot(Equal(expectedTime))
			g.Expect(p.SpotLastUpdated()).ToNot(Equal(expectedTime))
		}, 3*time.Second).Should(Succeed())

		// Price APIs still work
		price, ok := p.OnDemandPrice("Standard_D1")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically(">", 0))

		price, ok = p.SpotPrice("Standard_D1")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.10))
	})

	It("should not poll pricing data in non-public clouds", func() {
		fakePricingAPI.NextError.Set(fmt.Errorf("failed"))
		env := &auth.Environment{
			Cloud: cloud.AzureGovernment,
		}
		start := make(chan struct{}, 1)
		p := trackProvider(pricing.NewProvider(ctx, env, fakePricingAPI, "", start))
		start <- struct{}{}

		// TODO: If this were exported or we were in the same package we could just assert on the package variable rather than
		// duplicating it here
		expectedTime, _ := time.Parse(time.RFC3339, "2025-06-03T21:16:07Z")
		Consistently(func(g Gomega) {
			g.Expect(p.OnDemandLastUpdated()).To(Equal(expectedTime))
			g.Expect(p.SpotLastUpdated()).To(Equal(expectedTime))
		}, 3*time.Second).Should(Succeed())

		// Price APIs still work
		price, ok := p.OnDemandPrice("Standard_D1")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically(">", 0))
	})
})
