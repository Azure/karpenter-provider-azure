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
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/zone"
	"github.com/samber/lo"
)

//go:embed locations.json
var fakeLocationsJSON string

type ListLocationsInput struct {
	SubscriptionID string
	Options        *armsubscriptions.ClientListLocationsOptions
}

type SubscriptionsAPIBehavior struct {
	NewListLocationsPagerBehavior MockedFunction[ListLocationsInput, armsubscriptions.ClientListLocationsResponse]
	Locations                     sync.Map
	UseFakeData                   bool
}

var _ zone.SubscriptionsAPI = &SubscriptionsAPI{}

type SubscriptionsAPI struct {
	SubscriptionsAPIBehavior
}

func NewSubscriptionsAPI() (*SubscriptionsAPI, error) {
	result := &SubscriptionsAPI{
		SubscriptionsAPIBehavior: SubscriptionsAPIBehavior{
			NewListLocationsPagerBehavior: MockedFunction[ListLocationsInput, armsubscriptions.ClientListLocationsResponse]{},
			Locations:                     sync.Map{},
			UseFakeData:                   true,
		},
	}

	err := loadLocationsFromFile(result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (api *SubscriptionsAPI) Reset() {
	api.NewListLocationsPagerBehavior.Reset()
	api.Locations.Range(func(k, v any) bool {
		api.Locations.Delete(k)
		return true
	})
	if api.UseFakeData {
		err := loadLocationsFromFile(api)
		if err != nil {
			panic(err) // Not ideal, but shouldn't happen
		}
	}
}

func (api *SubscriptionsAPI) NewListLocationsPager(
	subscriptionID string,
	options *armsubscriptions.ClientListLocationsOptions,
) *runtime.Pager[armsubscriptions.ClientListLocationsResponse] {
	input := &ListLocationsInput{
		SubscriptionID: subscriptionID,
		Options:        options,
	}

	pagingHandler := runtime.PagingHandler[armsubscriptions.ClientListLocationsResponse]{
		More: func(page armsubscriptions.ClientListLocationsResponse) bool {
			return false // TODO: It might be ideal if we had a MockPager which sometimes simulated multiple pages of results to ensure we handle that correctly
		},
		Fetcher: func(ctx context.Context, _ *armsubscriptions.ClientListLocationsResponse) (armsubscriptions.ClientListLocationsResponse, error) {
			return api.NewListLocationsPagerBehavior.Invoke(input, func(input *ListLocationsInput) (armsubscriptions.ClientListLocationsResponse, error) {
				output := armsubscriptions.LocationListResult{
					Value: []*armsubscriptions.Location{},
				}

				api.Locations.Range(func(key, value any) bool {
					cast := value.(armsubscriptions.Location)
					output.Value = append(output.Value, &cast)
					return true
				})

				// Sort the result according to Name so that we have a stable base to write asserts upon
				sort.Slice(output.Value, func(i, j int) bool {
					l := output.Value[i]
					r := output.Value[j]
					return lo.FromPtr(l.Name) < lo.FromPtr(r.Name)
				})

				return armsubscriptions.ClientListLocationsResponse{
					LocationListResult: output,
				}, nil
			})
		},
	}

	return runtime.NewPager(pagingHandler)
}

func loadLocationsFromFile(api *SubscriptionsAPI) error {
	data := []byte(fakeLocationsJSON)

	var locations []*armsubscriptions.Location
	err := json.Unmarshal(data, &locations)
	if err != nil {
		return fmt.Errorf("failed to unmarshal locations JSON: %w", err)
	}

	for _, location := range locations {
		api.Locations.Store(lo.FromPtr(location.Name), *location)
	}

	return nil
}
