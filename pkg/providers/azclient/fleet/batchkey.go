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

// Package fleet implements Fleet batch key computation and request body construction
// for coalescing VM provisioning requests via the Azure Compute Fleet API.
package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
)

// DetermineBatchKey hashes a pre-built Fleet body and returns "{nodePoolName}/{hash16}".
func DetermineBatchKey(nodePoolName string, fleetBody *armcomputefleet.Fleet) (string, error) {
	if fleetBody == nil {
		return "", fmt.Errorf("nil fleet body")
	}

	data, err := json.Marshal(fleetBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal fleet body: %w", err)
	}

	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:8])

	return fmt.Sprintf("%s/%s", nodePoolName, hashHex), nil
}
