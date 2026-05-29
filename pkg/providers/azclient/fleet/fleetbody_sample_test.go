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
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateSample controls whether the sample JSON snapshot is rewritten from the current
// BuildFleetBody output. Pass `-update` when running the test to regenerate the file:
//
//	go test ./pkg/providers/azclient/fleet/... -run TestBuildFleetBody_SampleJSON -update
var updateSample = flag.Bool("update", false, "regenerate sample JSON fixtures under testdata/")

const sampleFleetBodyPath = "testdata/fleet_body_sample.json"

// TestBuildFleetBody_SampleJSON serializes a canonical Fleet body and asserts the JSON
// bytes match a checked-in snapshot. This catches structural drift that the field-level
// assertions in other tests would miss — particularly missing required ARM fields like
// NetworkAPIVersion, since the snapshot is what we'd actually send to the Fleet API.
//
// The input is deterministic (sorted slices, ordered maps via encoding/json sort) so the
// output JSON is stable across runs.
func TestBuildFleetBody_SampleJSON(t *testing.T) {
	fleet := buildCanonicalFleetForSample()

	got, err := json.MarshalIndent(fleet, "", "  ")
	require.NoError(t, err, "marshal must succeed")
	// Normalize trailing newline so editors and `git diff` are happy.
	got = append(got, '\n')

	if *updateSample {
		require.NoError(t, os.MkdirAll(filepath.Dir(sampleFleetBodyPath), 0o755))
		require.NoError(t, os.WriteFile(sampleFleetBodyPath, got, 0o644))
		t.Logf("sample file rewritten: %s", sampleFleetBodyPath)
		return
	}

	want, err := os.ReadFile(sampleFleetBodyPath)
	require.NoError(t, err,
		"sample file %s missing — run `go test ./pkg/providers/azclient/fleet/... -run TestBuildFleetBody_SampleJSON -update` to create it",
		sampleFleetBodyPath)

	if !bytes.Equal(got, want) {
		assert.Equal(t, string(want), string(got),
			"fleet body JSON drifted from sample snapshot. Review the diff carefully — "+
				"if the change is intentional, regenerate with `-update`.")
	}
}

// buildCanonicalFleetForSample produces a fully-populated Fleet body using deterministic
// inputs. It exercises spot priority, encryption-at-host, ephemeral OS disk,
// disk-encryption set, user-assigned identities, LB backend pools, and extensions —
// the full surface area we care about catching drift on.
func buildCanonicalFleetForSample() interface{} {
	fields := defaultFields()
	fields.CapacityType = "spot"
	fields.OSDiskType = "CacheDisk"
	fields.EncryptionAtHost = true
	fields.DiskEncryptionSetID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/diskEncryptionSets/des"
	fields.NodeIdentities = "/sub/rg/id1,/sub/rg/id2"
	price := float32(0.75)
	pools := []string{"/sub/rg/lb/pool1"}

	return BuildFleetBody(
		fields,
		5,
		defaultTags(),
		&price,
		"eastus2",
		pools,
		mkInstanceTypes("Standard_D4s_v3", "Standard_D8s_v3"),
		true,
		nil,
	)
}
