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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

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

// DetermineBatchKey computes a deterministic grouping key for a FleetVMProvisionRequest.
// Two requests batch into the same Fleet iff every field on BatchKeyFields matches.
//
// Per-VM fields (Tags, NodeClaimName) are intentionally excluded — otherwise every
// NodeClaim would land in its own Fleet and batching would be a no-op.
//
// This is the batcher.DetermineBatchKey[FleetVMProvisionRequest] implementation.
func DetermineBatchKey(req *FleetVMProvisionRequest) (string, error) {
	if req == nil || req.NodeClaim == nil || req.NodeClass == nil || req.LaunchTemplate == nil {
		return "", fmt.Errorf("nil request, nodeclaim, nodeclass, or launch template")
	}

	fields := BatchKeyFields{
		NodePoolName:  req.NodeClaim.Labels[karpv1.NodePoolLabelKey],
		CapacityType:  req.CapacityType,
		ImageID:       req.LaunchTemplate.ImageID,
		SubnetID:      req.LaunchTemplate.SubnetID,
		SSHPublicKey:  req.SSHPublicKey,
		AdminUsername: req.AdminUsername,
		CustomData:    req.LaunchTemplate.ScriptlessCustomData,
		OSDiskSizeGB:  int(req.LaunchTemplate.StorageProfileSizeGB),

		// OSDiskType is sourced from StorageProfilePlacement (the DiffDiskPlacement enum:
		// "CacheDisk" / "ResourceDisk" / "NvmeDisk", or "" when the disk is managed).
		//
		// This is the field that actually lands in the Fleet body's storageProfile, so the
		// hash directly reflects "can these requests share one Fleet?".
		//
		// Alternatives considered and rejected:
		//   (A) StorageProfileIsEphemeral bool — too coarse: two requests that both resolve
		//       to ephemeral but with different placements (e.g. CacheDisk vs NvmeDisk)
		//       would erroneously batch together; the Fleet body can only carry one
		//       placement, so the second request would silently get the first's choice.
		//   (C) Hash both IsEphemeral AND Placement — redundant. The boolean is derivable
		//       from placement ("" == managed, non-empty == ephemeral).
		OSDiskType: string(req.LaunchTemplate.StorageProfilePlacement),

		EncryptionAtHost:    req.NodeClass.GetEncryptionAtHost(),
		DiskEncryptionSetID: req.DiskEncryptionSetID,
		NodeIdentities:      joinSorted(req.NodeIdentities),
		NSG:                 req.NSG,
		CandidateSKUs:       sortedCopy(req.AcceptableSKUs),
		Zones:               sortedCopy(req.AcceptableZones),
	}

	blob, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("marshal batch key: %w", err)
	}

	sum := sha256.Sum256(blob)

	// Prefix with nodepool + capacityType so logs/metrics can tell batches apart at a glance,
	// mirroring the aksmachinesheaderbatch convention.
	return fmt.Sprintf("%s/%s/%x", fields.NodePoolName, fields.CapacityType, sum[:8]), nil
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func joinSorted(in []string) string {
	return strings.Join(sortedCopy(in), ",")
}
