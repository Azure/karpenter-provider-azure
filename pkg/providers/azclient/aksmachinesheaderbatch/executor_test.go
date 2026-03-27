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

package aksmachinesheaderbatch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
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
	panic("unexpected Get call in executor test")
}

func (r *recordingClient) NewListPager(
	string, string, string,
	*armcontainerservice.MachinesClientListOptions,
) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	panic("unexpected NewListPager call in executor test")
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

func tpl(vmSize string, zones []string, tags map[string]string) armcontainerservice.Machine {
	m := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}
	for _, z := range zones {
		m.Zones = append(m.Zones, &z)
	}
	if len(tags) > 0 {
		m.Properties.Tags = make(map[string]*string, len(tags))
		for k, v := range tags {
			m.Properties.Tags[k] = &v
		}
	}
	return m
}

func makeReq(name string, template armcontainerservice.Machine) *batcher.BatchedRequest[aksMachineCreatePayload, struct{}] {
	return &batcher.BatchedRequest[aksMachineCreatePayload, struct{}]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: "rg",
			resourceName:      "cluster",
			agentPoolName:     "pool",
			machineName:       name,
			machineBody:          template,
		},
		ResponseChan: make(chan *batcher.Response[struct{}], 1),
	}
}

func makeBatch(requests ...*batcher.BatchedRequest[aksMachineCreatePayload, struct{}]) *batcher.Batch[aksMachineCreatePayload, struct{}] {
	if len(requests) == 0 {
		return &batcher.Batch[aksMachineCreatePayload, struct{}]{}
	}
	return &batcher.Batch[aksMachineCreatePayload, struct{}]{
		Key:      determineBatchKey(&requests[0].Payload),
		Requests: requests,
	}
}

func awaitAll(t *testing.T, requests ...*batcher.BatchedRequest[aksMachineCreatePayload, struct{}]) []*batcher.Response[struct{}] {
	t.Helper()
	out := make([]*batcher.Response[struct{}], len(requests))
	for i, r := range requests {
		select {
		case resp := <-r.ResponseChan:
			out[i] = resp
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d (%s): timeout waiting for batch response", i, r.Payload.machineName)
		}
	}
	return out
}

// =====================================================================
// Executor unit tests — deterministic, no background loop
// =====================================================================

func TestExecutorSingleAPICallForBatch(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"env": "test"}))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, map[string]string{"env": "test"}))
	r3 := makeReq("m-3", tpl("Standard_D2s_v3", []string{"1", "2"}, map[string]string{"env": "staging"}))

	exec.executeBatch(makeBatch(r1, r2, r3))

	assert.Equal(t, int32(1), mock.count.Load(), "3 machines should produce exactly 1 API call")
	for i, resp := range awaitAll(t, r1, r2, r3) {
		assert.NoError(t, resp.Err, "request %d", i)
	}
}

func TestExecutorClearsPerMachineFieldsFromBody(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"k": "v"}))
	exec.executeBatch(makeBatch(r1))

	calls := mock.snapshot()
	require.Len(t, calls, 1)
	assert.Nil(t, calls[0].parameters.Zones, "zones must be nil in body (sent via header)")
	assert.Nil(t, calls[0].parameters.Properties.Tags, "tags must be nil in body (sent via header)")
	assert.Equal(t, "Standard_D2s_v3", *calls[0].parameters.Properties.Hardware.VMSize,
		"shared template fields must remain")
}

func TestExecutorAttachesPerMachineEntriesToContext(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"a": "1"}))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2", "3"}, map[string]string{"b": "2"}))
	exec.executeBatch(makeBatch(r1, r2))

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

func TestExecutorDistributesErrorToAllCallers(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{err: fmt.Errorf("azure boom")}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		require.Error(t, resp.Err)
		assert.Contains(t, resp.Err.Error(), "azure boom")
	}
}

// =====================================================================
// Full-stack integration — Client + Batcher + Executor
// =====================================================================

// Five concurrent BeginCreateWithBatch calls with the same template must
// result in fewer API calls than requests, proving batching works.
func TestConcurrentRequestsBatchThroughClient(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := NewClient(ctx, mock, batcher.Options{
		IdleTimeout:  200 * time.Millisecond,
		MaxTimeout:   5 * time.Second,
		MaxBatchSize: 50,
	})

	tmpl := tpl("Standard_D2s_v3", []string{"1"}, nil)

	const n = 5
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = client.BeginCreateWithBatch(ctx, "rg", "cluster", "pool", fmt.Sprintf("machine-%d", i), tmpl)
		}(i)
	}
	wg.Wait()

	calls := mock.count.Load()
	assert.GreaterOrEqual(t, calls, int32(1), "at least 1 API call")
	assert.Less(t, calls, int32(n), "fewer API calls than requests → batching worked")
}

// Requests to different resource paths (cluster/pool) must land in separate
// batches even if the machine template is identical.
func TestDifferentResourcePathsSeparateBatches(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{}
	exec := newExecutor(mock)

	tmpl := tpl("Standard_D2s_v3", []string{"1"}, nil)

	r1 := &batcher.BatchedRequest[aksMachineCreatePayload, struct{}]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: "rg-1",
			resourceName:      "cluster-1",
			agentPoolName:     "pool",
			machineName:       "m-1",
			machineBody:       tmpl,
		},
		ResponseChan: make(chan *batcher.Response[struct{}], 1),
	}
	r2 := &batcher.BatchedRequest[aksMachineCreatePayload, struct{}]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: "rg-2",
			resourceName:      "cluster-2",
			agentPoolName:     "pool",
			machineName:       "m-2",
			machineBody:       tmpl,
		},
		ResponseChan: make(chan *batcher.Response[struct{}], 1),
	}

	key1 := determineBatchKey(&r1.Payload)
	key2 := determineBatchKey(&r2.Payload)
	assert.NotEqual(t, key1, key2, "different resource paths should produce different batch keys")

	// Execute each as separate batch (as the batcher would)
	exec.executeBatch(&batcher.Batch[aksMachineCreatePayload, struct{}]{Key: key1, Requests: []*batcher.BatchedRequest[aksMachineCreatePayload, struct{}]{r1}})
	exec.executeBatch(&batcher.Batch[aksMachineCreatePayload, struct{}]{Key: key2, Requests: []*batcher.BatchedRequest[aksMachineCreatePayload, struct{}]{r2}})

	assert.Equal(t, int32(2), mock.count.Load(), "different resource paths → 2 API calls")

	calls := mock.snapshot()
	assert.Equal(t, "m-1", calls[0].machineName)
	assert.Equal(t, "m-2", calls[1].machineName)
}
