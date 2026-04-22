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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"
	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// recordingClient — thread-safe MachinesCreateAPI stub that records API calls
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

//nolint:unparam // vmSize is always the same today but kept as param for future test flexibility
func tpl(vmSize string, zones []string, tags map[string]string) *armcontainerservice.Machine {
	m := &armcontainerservice.Machine{
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

func makeReq(name string, template *armcontainerservice.Machine) *batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError] {
	return &batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: "rg",
			resourceName:      "cluster",
			agentPoolName:     "pool",
			machineName:       name,
			machineBody:       template,
		},
		ResponseChan: make(chan *batcher.Response[*offerings.HandlableError], 1),
	}
}

func makeBatch(requests ...*batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]) *batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError] {
	if len(requests) == 0 {
		return &batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]{}
	}
	return &batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]{
		Key: func() string { k, _ := determineBatchKey(&requests[0].Payload); return k }(),
		Requests: requests,
	}
}

func awaitAll(t *testing.T, requests ...*batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]) []*batcher.Response[*offerings.HandlableError] {
	t.Helper()
	out := make([]*batcher.Response[*offerings.HandlableError], len(requests))
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

	exec.executeBatch(context.Background(), makeBatch(r1, r2, r3))

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
	exec.executeBatch(context.Background(), makeBatch(r1))

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
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

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
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		// Plain error (not *azcore.ResponseError) → extractPerMachineErrors fails → operational error
		require.Error(t, resp.Err, "should be an operational error (parse failure)")
		assert.Nil(t, resp.Payload)
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
			_, _ = client.BeginCreateWithBatch(ctx, "rg", "cluster", "pool", fmt.Sprintf("machine-%d", i), tmpl)
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

	r1 := &batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: "rg-1",
			resourceName:      "cluster-1",
			agentPoolName:     "pool",
			machineName:       "m-1",
			machineBody:       tmpl,
		},
		ResponseChan: make(chan *batcher.Response[*offerings.HandlableError], 1),
	}
	r2 := &batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{
		Payload: aksMachineCreatePayload{
			resourceGroupName: "rg-2",
			resourceName:      "cluster-2",
			agentPoolName:     "pool",
			machineName:       "m-2",
			machineBody:       tmpl,
		},
		ResponseChan: make(chan *batcher.Response[*offerings.HandlableError], 1),
	}

	key1, _ := determineBatchKey(&r1.Payload)
	key2, _ := determineBatchKey(&r2.Payload)
	assert.NotEqual(t, key1, key2, "different resource paths should produce different batch keys")

	// Execute each as separate batch (as the batcher would)
	exec.executeBatch(context.Background(), &batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]{Key: key1, Requests: []*batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{r1}})
	exec.executeBatch(context.Background(), &batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]{Key: key2, Requests: []*batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{r2}})

	assert.Equal(t, int32(2), mock.count.Load(), "different resource paths → 2 API calls")

	calls := mock.snapshot()
	assert.Equal(t, "m-1", calls[0].machineName)
	assert.Equal(t, "m-2", calls[1].machineName)
}

// =====================================================================
// Batch error helpers for tests
// =====================================================================

type testErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Target  string `json:"target"`
}

// makeBatchClientError creates an *azcore.ResponseError simulating a 400 BatchMachineClientError
// with per-machine error details at the top level.
func makeBatchClientError(details []testErrorDetail) *azcore.ResponseError {
	body := struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details []testErrorDetail `json:"details"`
	}{
		Code:    "BatchMachineClientError",
		Message: "batch client error",
		Details: details,
	}
	bodyJSON, _ := json.Marshal(body)
	return &azcore.ResponseError{
		ErrorCode:  "BatchMachineClientError",
		StatusCode: http.StatusBadRequest,
		RawResponse: &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(bytes.NewReader(bodyJSON)),
		},
	}
}

// makeBatchInternalServerError creates an *azcore.ResponseError simulating a 500 BatchMachineInternalServerError
// with per-machine error details JSON-encoded in the message field.
func makeBatchInternalServerError(details []testErrorDetail) *azcore.ResponseError {
	innerJSON, _ := json.Marshal(struct {
		Details []testErrorDetail `json:"details"`
	}{Details: details})
	body := struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{
		Code:    "BatchMachineInternalServerError",
		Message: string(innerJSON),
	}
	bodyJSON, _ := json.Marshal(body)
	return &azcore.ResponseError{
		ErrorCode:  "BatchMachineInternalServerError",
		StatusCode: http.StatusInternalServerError,
		RawResponse: &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewReader(bodyJSON)),
		},
	}
}

// =====================================================================
// Per-machine batch error tests
// =====================================================================

func TestExecutorBatchClientError_PartialFailure(t *testing.T) {
	t.Parallel()
	// Simulate: 3 machines, only m-2 fails with client error. m-1 and m-3 succeed.
	mock := &recordingClient{err: makeBatchClientError([]testErrorDetail{
		{Code: "InvalidParameter", Message: "simulated client error for machine m-2", Target: "m-2"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	r3 := makeReq("m-3", tpl("Standard_D2s_v3", []string{"3"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2, r3))

	resps := awaitAll(t, r1, r2, r3)
	// m-1: success (no operational error, no API error)
	assert.NoError(t, resps[0].Err, "m-1 should have no operational error")
	assert.Nil(t, resps[0].Payload, "m-1 should have no API error")
	// m-2: per-machine API error (no operational error, APIError payload)
	assert.NoError(t, resps[1].Err, "m-2 should have no operational error")
	require.NotNil(t, resps[1].Payload, "m-2 should have an API error")
	assert.Equal(t, "InvalidParameter", resps[1].Payload.Code)
	// m-3: success
	assert.NoError(t, resps[2].Err, "m-3 should have no operational error")
	assert.Nil(t, resps[2].Payload, "m-3 should have no API error")
}

func TestExecutorBatchClientError_AllFail(t *testing.T) {
	t.Parallel()
	// Simulate: all machines fail with client + internal errors mixed
	mock := &recordingClient{err: makeBatchClientError([]testErrorDetail{
		{Code: "InvalidParameter", Message: "client error for m-1", Target: "m-1"},
		{Code: "InternalOperationError", Message: "internal error for m-2", Target: "m-2"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	resps := awaitAll(t, r1, r2)
	assert.NoError(t, resps[0].Err)
	require.NotNil(t, resps[0].Payload)
	assert.Equal(t, "InvalidParameter", resps[0].Payload.Code)
	assert.NoError(t, resps[1].Err)
	require.NotNil(t, resps[1].Payload)
	assert.Equal(t, "InternalOperationError", resps[1].Payload.Code)
}

func TestExecutorBatchInternalServerError_PartialFailure(t *testing.T) {
	t.Parallel()
	// Simulate: 500 internal server error, only m-1 fails. m-2 succeeds.
	mock := &recordingClient{err: makeBatchInternalServerError([]testErrorDetail{
		{Code: "InternalOperationError", Message: "internal error for m-1", Target: "m-1"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	resps := awaitAll(t, r1, r2)
	assert.NoError(t, resps[0].Err)
	require.NotNil(t, resps[0].Payload, "m-1 should have an API error detail")
	assert.Equal(t, "InternalOperationError", resps[0].Payload.Code)
	assert.NoError(t, resps[1].Err, "m-2 should have no operational error")
	assert.Nil(t, resps[1].Payload, "m-2 should succeed")
}

func TestExecutorBatchInternalServerError_AllFail(t *testing.T) {
	t.Parallel()
	mock := &recordingClient{err: makeBatchInternalServerError([]testErrorDetail{
		{Code: "InternalOperationError", Message: "error for m-1", Target: "m-1"},
		{Code: "InternalOperationError", Message: "error for m-2", Target: "m-2"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	resps := awaitAll(t, r1, r2)
	assert.NoError(t, resps[0].Err)
	require.NotNil(t, resps[0].Payload)
	assert.NoError(t, resps[1].Err)
	require.NotNil(t, resps[1].Payload)
}

func TestExecutorNonBatchError_FallsBackToDistributeAll(t *testing.T) {
	t.Parallel()
	// Non-batch error (plain error) → extractPerMachineErrors fails → operational error
	mock := &recordingClient{err: fmt.Errorf("plain error")}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		require.Error(t, resp.Err)
		assert.Nil(t, resp.Payload)
	}
}

func TestExecutorUnknownBatchErrorCode_DistributesAsSingleAPIError(t *testing.T) {
	t.Parallel()
	// ResponseError with unrecognized error code → non-batch error → single HandlableError to all
	mock := &recordingClient{err: func() error {
		body, _ := json.Marshal(map[string]any{"code": "SomeUnknownError", "message": "something broke"})
		return &azcore.ResponseError{
			ErrorCode:  "SomeUnknownError",
			StatusCode: http.StatusBadRequest,
			RawResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewReader(body)),
			},
		}
	}()}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		assert.NoError(t, resp.Err)
		require.NotNil(t, resp.Payload)
		assert.Equal(t, "SomeUnknownError", resp.Payload.Code)
	}
}

func TestExecutorBatchErrorMalformedBody_DistributesAsOperationalError(t *testing.T) {
	t.Parallel()
	// BatchMachineClientError but with malformed body → extractPerMachineErrors fails → operational error
	mock := &recordingClient{err: &azcore.ResponseError{
		ErrorCode:  "BatchMachineClientError",
		StatusCode: http.StatusBadRequest,
		RawResponse: &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(bytes.NewReader([]byte("not json"))),
		},
	}}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		require.Error(t, resp.Err)
		assert.Nil(t, resp.Payload)
	}
}

func TestExecutorBatchErrorNoRawResponse_DistributesAsOperationalError(t *testing.T) {
	t.Parallel()
	// BatchMachineClientError but without RawResponse → extractPerMachineErrors fails → operational error
	mock := &recordingClient{err: &azcore.ResponseError{
		ErrorCode:  "BatchMachineClientError",
		StatusCode: http.StatusBadRequest,
	}}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		require.Error(t, resp.Err)
		assert.Nil(t, resp.Payload)
	}
}
