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

package offerings

import (
	"context"

	"github.com/Azure/karpenter-provider-azure/pkg/cache"
)

// PerSKUError represents a per-VM-size error from the Fleet API response details.
// Defined in offerings (not fleet) to avoid circular imports — same pattern as HandlableError.
type PerSKUError struct {
	VMSize  string
	Zone    string
	Code    string
	Message string
}

// FleetErrorHandler classifies Fleet API errors and takes appropriate offerings cache actions.
type FleetErrorHandler struct {
	unavailableOfferings *cache.UnavailableOfferings
}

// NewFleetErrorHandler creates a handler for Fleet LRO errors.
func NewFleetErrorHandler(unavailableOfferings *cache.UnavailableOfferings) *FleetErrorHandler {
	return &FleetErrorHandler{
		unavailableOfferings: unavailableOfferings,
	}
}

// HandleFleetError processes a Fleet error and updates the ICE cache accordingly.
// Rules:
//   - Single-SKU + per-SKU details → return ICE, no backoff
//   - Multi-SKU + per-SKU details → log details, return ICE, no cache mutation
//   - Multi-SKU + no per-SKU details → return ICE + MarkBackoff(batchKey, 5min)
//   - LowPriorityQuotaHasBeenReached → MarkSpotUnavailableWithTTL(3min)
//   - Non-capacity errors → wrap as ICE, no cache mutation
func (h *FleetErrorHandler) HandleFleetError(
	ctx context.Context,
	batchKey string,
	skuCount int,
	perSKUErrors []PerSKUError,
	errorCode, errorMessage string,
) error {
	// TODO: implement error classification and cache actions per rules above
	return nil
}
