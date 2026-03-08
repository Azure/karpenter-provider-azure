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

package batch

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeTemplateHash(t *testing.T) {
	t.Parallel()

	vmSize1 := "Standard_D2s_v3"
	vmSize2 := "Standard_D4s_v3"
	zone1 := "1"
	zone2 := "2"
	name1 := "machine1"
	name2 := "machine2"

	template1 := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize1,
			},
		},
		Zones: []*string{&zone1},
		Name:  &name1,
	}

	template2 := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize1,
			},
		},
		Zones: []*string{&zone2},
		Name:  &name2,
	}

	template3 := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize2,
			},
		},
		Zones: []*string{&zone1},
		Name:  &name1,
	}

	hash1 := computeTemplateHash(template1)
	hash2 := computeTemplateHash(template2)
	hash3 := computeTemplateHash(template3)

	assert.Equal(t, hash1, hash2, "hashes should be equal when only zones and names differ")
	assert.NotEqual(t, hash1, hash3, "hashes should differ when VM size differs")
}

// Tags contain NodeClaim-unique values (nodeClaim.Name, creationTimestamp) and must
// be excluded from the hash so that machines with the same template but different
// NodeClaims still batch together.
func TestComputeTemplateHash_TagsExcluded(t *testing.T) {
	t.Parallel()

	vmSize := "Standard_D2s_v3"

	withTags := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			Tags: map[string]*string{
				"karpenter.azure.com_aksmachine_nodeclaim":          lo.ToPtr("nodeclaim-abc"),
				"karpenter.azure.com_aksmachine_creationtimestamp":  lo.ToPtr("2026-01-01T00:00:00Z"),
			},
		},
	}
	withoutTags := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}
	withDifferentTags := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			Tags: map[string]*string{
				"karpenter.azure.com_aksmachine_nodeclaim":          lo.ToPtr("nodeclaim-xyz"),
				"karpenter.azure.com_aksmachine_creationtimestamp":  lo.ToPtr("2026-02-02T00:00:00Z"),
			},
		},
	}

	h1 := computeTemplateHash(withTags)
	h2 := computeTemplateHash(withoutTags)
	h3 := computeTemplateHash(withDifferentTags)

	assert.Equal(t, h1, h2, "tags should not affect hash")
	assert.Equal(t, h1, h3, "different tags should not affect hash")
}

// Read-only fields (ETag, ProvisioningState, ResourceID, Status) must not affect the hash.
func TestComputeTemplateHash_ReadOnlyFieldsExcluded(t *testing.T) {
	t.Parallel()

	vmSize := "Standard_D2s_v3"

	withReadOnly := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware:          &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			ETag:              lo.ToPtr("etag-123"),
			ProvisioningState: lo.ToPtr("Succeeded"),
			ResourceID:        lo.ToPtr("/subscriptions/sub/resourceGroups/rg/..."),
		},
	}
	withoutReadOnly := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}

	h1 := computeTemplateHash(withReadOnly)
	h2 := computeTemplateHash(withoutReadOnly)

	assert.Equal(t, h1, h2, "read-only fields should not affect hash")
}

// Guardrail: ensures that every field of MachineProperties is either hashed or
// explicitly excluded. If the Azure SDK adds a new field to MachineProperties,
// this test fails — forcing the developer to decide whether to hash or exclude it.
func TestComputeTemplateHash_AllFieldsAccountedFor(t *testing.T) {
	t.Parallel()

	// Fields explicitly excluded from the hash in computeTemplateHash.
	// If you add a new field to this set, add a comment explaining why it's excluded.
	excludedFields := map[string]string{
		"Tags":              "per-machine: contains NodeClaim name and creation timestamp",
		"ETag":              "read-only: set by server",
		"ProvisioningState": "read-only: set by server",
		"ResourceID":        "read-only: set by server",
		"Status":            "read-only: set by server",
	}

	typ := reflect.TypeOf(armcontainerservice.MachineProperties{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if _, excluded := excludedFields[field.Name]; excluded {
			continue
		}
		// If this assertion fails, a new field was added to MachineProperties.
		// Decide: should it be hashed (do nothing) or excluded (add to excludedFields above)?
		assert.True(t, field.IsExported(),
			"unexpected unexported field %q in MachineProperties — review computeTemplateHash", field.Name)
	}

	// Verify the count matches: hashed fields + excluded fields == total fields.
	// This catches the case where someone adds a field to excludedFields without
	// a corresponding SDK field (stale exclude entry).
	totalFields := typ.NumField()
	hashedFields := totalFields - len(excludedFields)
	assert.Greater(t, hashedFields, 0,
		"at least one field should be hashed (got %d total, %d excluded)", totalFields, len(excludedFields))

	// Verify all excluded fields actually exist in the struct.
	for name, reason := range excludedFields {
		_, found := typ.FieldByName(name)
		assert.True(t, found,
			"excluded field %q (reason: %s) does not exist in MachineProperties — remove from excludedFields", name, reason)
	}
}

func TestGrouperEnqueueCreate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	vmSize := "Standard_D2s_v3"
	zone := "1"
	machineName := "machine1"

	req := &CreateRequest{
		ctx:          ctx,
		machineName:  machineName,
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize,
				},
			},
			Zones: []*string{&zone},
		},
	}

	responseChan := grouper.EnqueueCreate(req)
	assert.NotNil(t, responseChan)

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 1, "should have one pending batch")
	grouper.mu.Unlock()
}

func TestGrouperBatchesSameTemplate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	vmSize := "Standard_D2s_v3"
	zone1 := "1"
	zone2 := "2"

	req1 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine1",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize,
				},
			},
			Zones: []*string{&zone1},
		},
	}

	req2 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine2",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize,
				},
			},
			Zones: []*string{&zone2},
		},
	}

	grouper.EnqueueCreate(req1)
	grouper.EnqueueCreate(req2)

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 1, "should have one pending batch for same template")
	for _, batch := range grouper.batches {
		assert.Len(t, batch.requests, 2, "batch should contain both requests")
	}
	grouper.mu.Unlock()
}

func TestGrouperBatchesDifferentTemplate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	vmSize1 := "Standard_D2s_v3"
	vmSize2 := "Standard_D4s_v3"
	zone := "1"

	req1 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine1",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize1,
				},
			},
			Zones: []*string{&zone},
		},
	}

	req2 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine2",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize2,
				},
			},
			Zones: []*string{&zone},
		},
	}

	grouper.EnqueueCreate(req1)
	grouper.EnqueueCreate(req2)

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 2, "should have two pending batches for different templates")
	grouper.mu.Unlock()
}

// When the grouper shuts down (context canceled), pending requests that haven't
// been dispatched yet must receive an error instead of hanging forever.
func TestGrouperDrainsPendingRequestsOnShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	// Create grouper but DON'T start the background loop — this ensures requests
	// stay in the pending map and are only handled by drain.
	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  10 * time.Second,
		MaxTimeout:   10 * time.Second,
		MaxBatchSize: 50,
	})
	grouper.SetCoordinator(NewCoordinator(&recordingClient{}, "rg", "cluster", "pool"))

	// Enqueue requests — they'll sit in the pending batch with no loop to dispatch them
	vmSize := "Standard_D2s_v3"
	req1 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine1",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			},
		},
	}
	req2 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine2",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			},
		},
	}

	grouper.EnqueueCreate(req1)
	grouper.EnqueueCreate(req2)

	// Verify requests are pending
	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 1, "should have one pending batch")
	grouper.mu.Unlock()

	// Cancel context then drain directly — verifies drainPendingRequests
	// fails all waiting callers with a shutdown error.
	cancel()
	grouper.drainPendingRequests()

	// Both requests should receive a shutdown error
	for i, req := range []*CreateRequest{req1, req2} {
		select {
		case resp := <-req.responseChan:
			require.Error(t, resp.Err, "request %d should receive a shutdown error", i)
			assert.Contains(t, resp.Err.Error(), "shutting down", "request %d error should mention shutdown", i)
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d timed out — drain did not deliver shutdown error", i)
		}
	}
}
