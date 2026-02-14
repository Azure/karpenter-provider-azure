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

// Why these tests live here instead of in a suite_*_batch_test.go file:
//
// The provisioner creates NodeClaims in a serial for-loop. Each Create() call
// blocks until the batch idle timeout fires, so every batch window closes with
// exactly 1 machine — batching never actually happens. Suite-level assertions
// like "callCount >= 1" are trivially true whether batching is on or off.
//
// These tests target what's unique to the batch system:
//   - Coordinator: 1 API call per batch, body stripped, context carries entries, errors fan out
//   - Grouper+Coordinator: same-template grouping, different-template splitting
//   - Concurrent client: real goroutines prove the background loop actually batches (calls < N)
package batch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// recordingClient — thread-safe AKSMachinesAPI stub that records API calls
// ---------------------------------------------------------------------------

type recordingClient struct {
	mu    sync.Mutex
	calls []recordedCall
	count atomic.Int32
	err   error // if non-nil, BeginCreateOrUpdate returns this
}

type recordedCall struct {
	machineName string
	parameters  armcontainerservice.Machine
	ctx         context.Context
}

func (r *recordingClient) BeginCreateOrUpdate(
	ctx context.Context,
	resourceGroupName, resourceName, agentPoolName, aksMachineName string,
	parameters armcontainerservice.Machine,
	_ *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions,
) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	r.count.Add(1)
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{
		machineName: aksMachineName,
		parameters:  parameters,
		ctx:         ctx,
	})
	r.mu.Unlock()
	return nil, r.err
}

func (r *recordingClient) Get(
	context.Context, string, string, string, string,
	*armcontainerservice.MachinesClientGetOptions,
) (armcontainerservice.MachinesClientGetResponse, error) {
	panic("unexpected Get call in coordinator test")
}

func (r *recordingClient) NewListPager(
	string, string, string,
	*armcontainerservice.MachinesClientListOptions,
) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	panic("unexpected NewListPager call in coordinator test")
}

func (r *recordingClient) snapshot() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// tpl builds an armcontainerservice.Machine with the given VM size, zones, and tags.
func tpl(vmSize string, zones []string, tags map[string]string) armcontainerservice.Machine {
	m := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}
	for _, z := range zones {
		z := z
		m.Zones = append(m.Zones, &z)
	}
	if len(tags) > 0 {
		m.Properties.Tags = make(map[string]*string, len(tags))
		for k, v := range tags {
			v := v
			m.Properties.Tags[k] = &v
		}
	}
	return m
}

// cReq builds a CreateRequest with a buffered response channel.
func cReq(ctx context.Context, name string, template armcontainerservice.Machine) *CreateRequest {
	return &CreateRequest{
		ctx:          ctx,
		machineName:  name,
		template:     template,
		responseChan: make(chan *CreateResponse, 1),
	}
}

// pBatch builds a PendingBatch from requests, using the first request's template.
func pBatch(requests ...*CreateRequest) *PendingBatch {
	if len(requests) == 0 {
		return &PendingBatch{}
	}
	return &PendingBatch{
		templateHash: computeTemplateHash(&requests[0].template),
		template:     requests[0].template,
		requests:     requests,
	}
}

// awaitAll drains every request's response channel with a generous timeout.
func awaitAll(t *testing.T, requests ...*CreateRequest) []*CreateResponse {
	t.Helper()
	out := make([]*CreateResponse, len(requests))
	for i, r := range requests {
		select {
		case resp := <-r.responseChan:
			out[i] = resp
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d (%s): timeout waiting for batch response", i, r.machineName)
		}
	}
	return out
}

// =====================================================================
// Coordinator unit tests — deterministic, no background loop
// =====================================================================

// Three machines in one batch produce exactly one API call; every response
// channel receives a non-error result with a batch ID.
func TestCoordinatorSingleAPICallForBatch(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	coord := NewCoordinator(mock, "rg", "cluster", "pool")

	ctx := context.Background()
	r1 := cReq(ctx, "m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"env": "test"}))
	r2 := cReq(ctx, "m-2", tpl("Standard_D2s_v3", []string{"2"}, map[string]string{"env": "test"}))
	r3 := cReq(ctx, "m-3", tpl("Standard_D2s_v3", []string{"1", "2"}, map[string]string{"env": "staging"}))

	coord.ExecuteBatch(pBatch(r1, r2, r3))

	assert.Equal(t, int32(1), mock.count.Load(), "3 machines should produce exactly 1 API call")
	for i, resp := range awaitAll(t, r1, r2, r3) {
		assert.NoError(t, resp.Err, "request %d", i)
		assert.NotEmpty(t, resp.BatchID, "request %d should carry a batchID", i)
	}
}

// The body sent to the API must have nil Zones and nil Tags (those
// travel via the BatchPutMachine header), but shared fields like
// VMSize remain.
func TestCoordinatorClearsPerMachineFieldsFromBody(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	coord := NewCoordinator(mock, "rg", "cluster", "pool")

	r1 := cReq(context.Background(), "m-1",
		tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"k": "v"}))
	coord.ExecuteBatch(pBatch(r1))

	calls := mock.snapshot()
	require.Len(t, calls, 1)
	assert.Nil(t, calls[0].parameters.Zones, "zones must be nil in body (sent via header)")
	assert.Nil(t, calls[0].parameters.Properties.Tags, "tags must be nil in body (sent via header)")
	assert.Equal(t, "Standard_D2s_v3", *calls[0].parameters.Properties.Hardware.VMSize,
		"shared template fields must remain")
}

// The context passed to BeginCreateOrUpdate must carry per-machine
// MachineEntry data (zones, tags, name) via WithFakeBatchEntries so
// that the fake can reconstruct individual machines.
func TestCoordinatorAttachesPerMachineEntriesToContext(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	coord := NewCoordinator(mock, "rg", "cluster", "pool")

	r1 := cReq(context.Background(), "m-1",
		tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"a": "1"}))
	r2 := cReq(context.Background(), "m-2",
		tpl("Standard_D2s_v3", []string{"2", "3"}, map[string]string{"b": "2"}))
	coord.ExecuteBatch(pBatch(r1, r2))

	calls := mock.snapshot()
	require.Len(t, calls, 1)

	entries := FakeBatchEntriesFromContext(calls[0].ctx)
	require.Len(t, entries, 2, "context should carry per-machine entries")

	assert.Equal(t, "m-1", entries[0].MachineName)
	assert.Equal(t, []string{"1"}, entries[0].Zones)
	assert.Equal(t, map[string]string{"a": "1"}, entries[0].Tags)

	assert.Equal(t, "m-2", entries[1].MachineName)
	assert.Equal(t, []string{"2", "3"}, entries[1].Zones)
	assert.Equal(t, map[string]string{"b": "2"}, entries[1].Tags)
}

// When the API call fails, every request's channel receives the error.
func TestCoordinatorDistributesErrorToAllCallers(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{err: fmt.Errorf("azure boom")}
	coord := NewCoordinator(mock, "rg", "cluster", "pool")

	r1 := cReq(context.Background(), "m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := cReq(context.Background(), "m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	coord.ExecuteBatch(pBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		require.Error(t, resp.Err)
		assert.Contains(t, resp.Err.Error(), "azure boom")
	}
}

// =====================================================================
// Grouper + Coordinator — deterministic (manual executeBatches)
// =====================================================================

// Five same-template requests enqueued into the grouper then manually
// flushed produce exactly one API call.
func TestEnqueuedSameTemplateBatchesToSingleCall(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	ctx := context.Background()

	g := NewGrouper(ctx, Options{
		IdleTimeout: time.Second, MaxTimeout: time.Second, MaxBatchSize: 50,
	})
	g.SetCoordinator(NewCoordinator(mock, "rg", "cluster", "pool"))

	tmpl := tpl("Standard_D2s_v3", []string{"1"}, nil)
	reqs := make([]*CreateRequest, 5)
	for i := range reqs {
		reqs[i] = cReq(ctx, fmt.Sprintf("m-%d", i), tmpl)
		g.EnqueueCreate(reqs[i])
	}

	g.executeBatches()
	awaitAll(t, reqs...)

	assert.Equal(t, int32(1), mock.count.Load(), "5 same-template requests → 1 API call")
}

// Two distinct VM sizes enqueued in the same window produce two batches
// (two API calls).
func TestEnqueuedDifferentTemplatesSeparateBatches(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	ctx := context.Background()

	g := NewGrouper(ctx, Options{
		IdleTimeout: time.Second, MaxTimeout: time.Second, MaxBatchSize: 50,
	})
	g.SetCoordinator(NewCoordinator(mock, "rg", "cluster", "pool"))

	tmplA := tpl("Standard_D2s_v3", []string{"1"}, nil)
	tmplB := tpl("Standard_D4s_v3", []string{"1"}, nil) // different VM size

	r1 := cReq(ctx, "a-1", tmplA)
	r2 := cReq(ctx, "a-2", tmplA)
	r3 := cReq(ctx, "b-1", tmplB)

	g.EnqueueCreate(r1)
	g.EnqueueCreate(r2)
	g.EnqueueCreate(r3)

	g.executeBatches()
	awaitAll(t, r1, r2, r3)

	assert.Equal(t, int32(2), mock.count.Load(), "2 distinct templates → 2 API calls")
}

// =====================================================================
// Full-stack integration — background loop, real timers, concurrency
// =====================================================================

// Five concurrent BeginCreateOrUpdate calls through the BatchingMachinesClient
// with the same template must result in fewer API calls than requests,
// proving the background grouper actually batches them.
func TestConcurrentRequestsBatchThroughClient(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g := NewGrouper(ctx, Options{
		IdleTimeout:  200 * time.Millisecond,
		MaxTimeout:   5 * time.Second,
		MaxBatchSize: 50,
	})
	g.SetCoordinator(NewCoordinator(mock, "rg", "cluster", "pool"))
	g.Start()

	client := NewBatchingMachinesClient(mock, g, "rg", "cluster", "pool")
	tmpl := tpl("Standard_D2s_v3", []string{"1"}, nil)

	const n = 5
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _ = client.BeginCreateOrUpdate(
				ctx, "rg", "cluster", "pool",
				fmt.Sprintf("machine-%d", i), tmpl, nil,
			)
		}(i)
	}
	wg.Wait()

	calls := mock.count.Load()
	assert.GreaterOrEqual(t, calls, int32(1), "at least 1 API call")
	assert.Less(t, calls, int32(n), "fewer API calls than requests → batching worked")
}
