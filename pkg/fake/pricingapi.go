// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fake

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/karpenter/pkg/providers/pricing/client"
)

const Region = "eastus"

type PricingAPI struct {
	client.PricingAPI
	PricingBehavior
}
type PricingBehavior struct {
	NextError         AtomicError
	ProductsPricePage AtomicPtr[client.ProductsPricePage]
}

func (p *PricingAPI) Reset() {
	p.NextError.Reset()
	p.ProductsPricePage.Reset()
}

func (p *PricingAPI) GetProductsPricePages(_ context.Context, _ []*client.Filter, fn func(output *client.ProductsPricePage)) error {
	if !p.NextError.IsNil() {
		return p.NextError.Get()
	}
	if !p.ProductsPricePage.IsNil() {
		fn(p.ProductsPricePage.Clone())
		return nil
	}
	// fail if the test doesn't provide specific data which causes our pricing provider to use its static price list
	return errors.New("no pricing data provided")
}

func NewProductPrice(instanceType string, price float64) client.Item {
	return client.Item{
		ArmSkuName:  instanceType,
		RetailPrice: price,
	}
}

func NewSpotProductPrice(instanceType string, price float64) client.Item {
	return client.Item{
		SkuName:     fmt.Sprintf("%s %s", instanceType, "Spot"),
		ArmSkuName:  instanceType,
		RetailPrice: price,
	}
}
