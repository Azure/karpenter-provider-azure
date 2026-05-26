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

// BatchKeyFields contains all fields that determine batch grouping.
// Requests with identical BatchKeyFields land in the same Fleet.
type BatchKeyFields struct {
	NodePoolName        string
	CapacityType        string
	ImageID             string
	SubnetID            string
	SSHPublicKey        string
	AdminUsername       string
	CustomData          string
	OSDiskSizeGB        int
	OSDiskType          string
	EncryptionAtHost    bool
	DiskEncryptionSetID string
	NodeIdentities      string   // sorted, comma-joined
	NSG                 string
	CandidateSKUs       []string // sorted alphabetically before hashing
	Zones               []string // sorted alphabetically before hashing
}

// DetermineBatchKey computes a SHA-256 hash (truncated to 8 hex chars) over BatchKeyFields.
// This is the batcher.DetermineBatchKey[FleetCreateRequest] implementation.
func DetermineBatchKey(req *FleetCreateRequest) (string, error) {
	// TODO: extract BatchKeyFields from req, sort SKUs/zones, compute SHA-256, return first 8 hex chars
	return "", nil
}
