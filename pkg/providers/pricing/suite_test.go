// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pricing_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "knative.dev/pkg/logging/testing"

	"github.com/Azure/karpenter/pkg/fake"
	"github.com/Azure/karpenter/pkg/providers/pricing"
	"github.com/Azure/karpenter/pkg/providers/pricing/client"
)

var ctx context.Context
var stop context.CancelFunc

var fakePricingAPI *fake.PricingAPI
var pricingProvider *pricing.Provider

func TestAzure(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Providers/Pricing/Azure")
}

var _ = BeforeSuite(func() {
	ctx, stop = context.WithCancel(ctx)

	fakePricingAPI = &fake.PricingAPI{}
	pricingProvider = pricing.NewProvider(ctx, fakePricingAPI, "", make(chan struct{}))
})

var _ = AfterSuite(func() {
	stop()
})

var _ = BeforeEach(func() {
	fakePricingAPI.Reset()
})

var _ = Describe("Pricing", func() {
	BeforeEach(func() {
		fakePricingAPI.Reset()
	})
	It("should return static on-demand data if pricing API fails", func() {
		fakePricingAPI.NextError.Set(fmt.Errorf("failed"))
		p := pricing.NewProvider(ctx, fakePricingAPI, "", make(chan struct{}))
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
		p := pricing.NewProvider(ctx, fakePricingAPI, "", make(chan struct{}))
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
		p := pricing.NewProvider(ctx, fakePricingAPI, "", make(chan struct{}))
		Eventually(func() bool { return p.SpotLastUpdated().After(updateStart) }).Should(BeTrue())

		price, ok := p.SpotPrice("Standard_D1")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.10))

		price, ok = p.SpotPrice("Standard_D14")
		Expect(ok).To(BeTrue())
		Expect(price).To(BeNumerically("==", 1.13))
	})
})
