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

package fleet

import (
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
)

// ParseFleetError extracts structured error information from a Fleet LRO failure.
// Returns the top-level code/message, per-SKU errors if available, and whether this is a capacity error.
func ParseFleetError(err error) (code, message string, perSKU []offerings.PerSKUError, isCapacity bool) {
	// TODO: extract code/message from azcore.ResponseError
	// Parse details[] for per-SKU errors (target → VMSize)
	// Classify capacity codes: AllocationFailed, OverconstrainedAllocationRequest,
	//   ZonalAllocationFailed, OverconstrainedZonalAllocationRequest, SKUNotAvailable
	return "", "", nil, false
}
