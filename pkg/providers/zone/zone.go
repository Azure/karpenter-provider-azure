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

package zone

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const maxFailures = 10

// SubscriptionsAPI defines the interface for Azure Subscriptions client operations
type SubscriptionsAPI interface {
	NewListLocationsPager(
		subscriptionID string,
		options *armsubscriptions.ClientListLocationsOptions,
	) *runtime.Pager[armsubscriptions.ClientListLocationsResponse]
}

// Provider handles zone support detection for Azure regions
type Provider struct {
	subscriptionsAPI SubscriptionsAPI
	subscriptionID   string

	// Cached zone support data - maps region name to zone support boolean
	zoneSupport map[string]bool
	hasLoaded   bool
	// failures is the number of times loading zone support from the Azure API has failed
	failures int
	mu       sync.Mutex
}

// NewProvider creates a new zone provider
func NewProvider(subscriptionsAPI SubscriptionsAPI, subscriptionID string) *Provider {
	return &Provider{
		subscriptionsAPI: subscriptionsAPI,
		subscriptionID:   subscriptionID,
		zoneSupport:      lo.Assign(fallbackZonalRegions), // deepcopy

	}
}

// SupportsZones returns true if the given region supports availability zones
func (p *Provider) SupportsZones(ctx context.Context, region string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.hasLoaded && p.failures < maxFailures {
		// Try to load zone support data from Azure API
		if err := p.loadFromAzure(ctx); err != nil {
			p.failures++
			log.FromContext(ctx).Error(err, "failed to load zone support from Azure API, falling back to hardcoded list")
		} else {
			p.hasLoaded = true
		}
	}

	return p.zoneSupport[region] // if cache doesn't have our region, assume no zone support
}

// loadFromAzure discovers zone support by calling Azure Subscriptions API
func (p *Provider) loadFromAzure(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.V(1).Info("discovering zone support for regions", "subscriptionID", p.subscriptionID)

	pager := p.subscriptionsAPI.NewListLocationsPager(p.subscriptionID, nil)
	result := make(map[string]bool)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list Azure locations: %w", err)
		}

		for _, location := range page.Value {
			locationName := lo.FromPtr(location.Name)
			if locationName == "" {
				continue
			}

			result[locationName] = len(location.AvailabilityZoneMappings) > 0
		}
	}

	log.Info("discovered zone support for regions", "regionCount", len(result))
	p.zoneSupport = lo.Assign(p.zoneSupport, result) // Merge with existing cache in case some regions are not returned
	return nil
}

// TODO: We may be able to remove this fallback entirely if we have data that suggests this API is very reliable
// Hardcoded fallback list of zonal regions - used when Azure API is unavailable
// Source: https://learn.microsoft.com/en-us/azure/reliability/regions-list#azure-regions-list-1
var fallbackZonalRegions = map[string]bool{
	// Special
	"centraluseuap": true,
	"eastus2euap":   true,
	// Americas
	"brazilsouth":    true,
	"canadacentral":  true,
	"centralus":      true,
	"eastus":         true,
	"eastus2":        true,
	"southcentralus": true,
	"usgovvirginia":  true,
	"westus2":        true,
	"westus3":        true,
	"chilecentral":   true,
	"mexicocentral":  true,
	// Europe
	"francecentral":      true,
	"italynorth":         true,
	"germanywestcentral": true,
	"norwayeast":         true,
	"northeurope":        true,
	"uksouth":            true,
	"westeurope":         true,
	"swedencentral":      true,
	"switzerlandnorth":   true,
	"polandcentral":      true,
	"spaincentral":       true,
	// Middle East
	"qatarcentral":  true,
	"uaenorth":      true,
	"israelcentral": true,
	// Africa
	"southafricanorth": true,
	// Asia Pacific
	"australiaeast":    true,
	"centralindia":     true,
	"japaneast":        true,
	"koreacentral":     true,
	"southeastasia":    true,
	"eastasia":         true,
	"chinanorth3":      true,
	"indonesiacentral": true,
	"japanwest":        true,
	"newzealandnorth":  true,
}
