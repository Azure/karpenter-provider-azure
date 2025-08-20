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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/samber/lo"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// We don't want to retry too aggressively here because this API is somewhat slow,
// but at the same time we want to wake back up eventually and try again in the case of an outage.
// These values were picked somewhat arbitrarily to achieve that.
const (
	maxFailuresPerWindow = 10
	windowBackoff        = 60 * time.Minute
)

type Clock interface {
	Now() time.Time
}

// SubscriptionsAPI defines the interface for Azure Subscriptions client operations
type SubscriptionsAPI interface {
	NewListLocationsPager(
		subscriptionID string,
		options *armsubscriptions.ClientListLocationsOptions,
	) *runtime.Pager[armsubscriptions.ClientListLocationsResponse]
}

// Provider handles zone support detection for Azure regions
// TODO: This provider is currently unused. Keeping it around for now though as we will likely want to adapt it
// to provide physical to logical zone mappings.
type Provider struct {
	subscriptionsAPI SubscriptionsAPI
	subscriptionID   string
	clock            Clock

	// Cached zone support data - maps region name to zone support boolean
	zoneSupport map[string]bool
	hasLoaded   bool
	// failures is the number of times loading zone support from the Azure API has failed
	failures    int
	lastAttempt time.Time
	mu          sync.Mutex
}

// NewProvider creates a new zone provider
func NewProvider(
	subscriptionsAPI SubscriptionsAPI,
	clock Clock,
	subscriptionID string,
) *Provider {
	result := &Provider{
		subscriptionsAPI: subscriptionsAPI,
		subscriptionID:   subscriptionID,
		clock:            clock,
		zoneSupport:      lo.Assign(fallbackZonalRegions), // deepcopy
	}

	return result
}

// SupportsZones returns true if the given region supports availability zones
func (p *Provider) SupportsZones(ctx context.Context, region string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// NOTE: We considered doing this in a separate goroutine or inline on provider construction but
	// we want:
	// 1. To block provisioning until we've at least attempted to load zone support data from the API once.
	// 2. To avoid blocking provisioning forever and eventually fall back to the hardcoded list.
	// It seems like this is the simplest way to accomplish that.
	if !p.hasLoaded && p.shouldTryAgain() {
		// Try to load zone support data from Azure API
		if err := p.loadFromAzure(ctx); err != nil {
			p.failures++
			p.lastAttempt = p.clock.Now()
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

// shouldTryAgain determines if the provider should attempt to load zone support data again
// after failures have happened.
func (p *Provider) shouldTryAgain() bool {
	now := p.clock.Now()
	if p.lastAttempt.Add(windowBackoff).Before(now) {
		p.failures = 0
		return true
	}

	if p.failures < maxFailuresPerWindow {
		return true
	}

	return false
}

// TODO: We may be able to remove this fallback entirely if we have data that suggests this API is very reliable
// Hardcoded fallback list of zonal regions - used when Azure API is unavailable
// Source: https://learn.microsoft.com/en-us/azure/reliability/regions-list#azure-regions-list-1
var fallbackZonalRegions = map[string]bool{
	// Special
	"eastus2euap": true,
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
	"austriaeast":        true,
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
	"malaysiawest":     true,
}
