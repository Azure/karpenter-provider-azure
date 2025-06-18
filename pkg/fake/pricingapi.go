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

package fake

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/karpenter-provider-azure/pkg/providers/pricing/client"
)

const Region = "southcentralus"
const RegionNonZonal = "westcentralus"

type PricingAPI struct {
	client.PricingAPI
	PricingBehavior
}
type PricingBehavior struct {
	NextError         AtomicError
	ProductsPricePage AtomicPtr[client.ProductsPricePage]
}

// assert that the fake implements the interface
var _ client.PricingAPI = &PricingAPI{}

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
