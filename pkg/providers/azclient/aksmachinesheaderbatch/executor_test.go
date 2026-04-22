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
	"github.com/onsi/gomega"
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
		Key:      func() string { k, _ := determineBatchKey(&requests[0].Payload); return k }(),
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
	g := gomega.NewWithT(t)
	mock := &recordingClient{}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"env": "test"}))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, map[string]string{"env": "test"}))
	r3 := makeReq("m-3", tpl("Standard_D2s_v3", []string{"1", "2"}, map[string]string{"env": "staging"}))

	exec.executeBatch(context.Background(), makeBatch(r1, r2, r3))

	g.Expect(mock.count.Load()).To(gomega.Equal(int32(1)), "3 machines should produce exactly 1 API call")
	for i, resp := range awaitAll(t, r1, r2, r3) {
		g.Expect(resp.Err).ToNot(gomega.HaveOccurred(), "request %d", i)
	}
}

func TestExecutorClearsPerMachineFieldsFromBody(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	mock := &recordingClient{}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"k": "v"}))
	exec.executeBatch(context.Background(), makeBatch(r1))

	calls := mock.snapshot()
	g.Expect(calls).To(gomega.HaveLen(1))
	g.Expect(calls[0].parameters.Zones).To(gomega.BeNil(), "zones must be nil in body (sent via header)")
	g.Expect(calls[0].parameters.Properties.Tags).To(gomega.BeNil(), "tags must be nil in body (sent via header)")
	g.Expect(*calls[0].parameters.Properties.Hardware.VMSize).To(gomega.Equal("Standard_D2s_v3"),
		"shared template fields must remain")
}

func TestExecutorAttachesPerMachineEntriesToContext(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	mock := &recordingClient{}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, map[string]string{"a": "1"}))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2", "3"}, map[string]string{"b": "2"}))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	calls := mock.snapshot()
	g.Expect(calls).To(gomega.HaveLen(1))

	entries := FakeBatchEntriesFromContext(calls[0].ctx)
	g.Expect(entries).To(gomega.HaveLen(2), "context should carry per-machine entries")

	g.Expect(entries[0].MachineName).To(gomega.Equal("m-1"))
	g.Expect(entries[0].Zones).To(gomega.Equal([]string{"1"}))
	g.Expect(entries[0].Tags).To(gomega.Equal(map[string]string{"a": "1"}))

	g.Expect(entries[1].MachineName).To(gomega.Equal("m-2"))
	g.Expect(entries[1].Zones).To(gomega.Equal([]string{"2", "3"}))
	g.Expect(entries[1].Tags).To(gomega.Equal(map[string]string{"b": "2"}))
}

func TestExecutorDistributesErrorToAllCallers(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	mock := &recordingClient{err: fmt.Errorf("azure boom")}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		// Plain error (not *azcore.ResponseError) → extractPerMachineErrors fails → operational error
		g.Expect(resp.Err).To(gomega.HaveOccurred(), "should be an operational error (parse failure)")
		g.Expect(resp.Payload).To(gomega.BeNil())
	}
}

// =====================================================================
// Full-stack integration — Client + Batcher + Executor
// =====================================================================

// Five concurrent BeginCreateWithBatch calls with the same template must
// result in fewer API calls than requests, proving batching works.
func TestConcurrentRequestsBatchThroughClient(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
	g.Expect(calls).To(gomega.BeNumerically(">=", int32(1)), "at least 1 API call")
	g.Expect(calls).To(gomega.BeNumerically("<", int32(n)), "fewer API calls than requests → batching worked")
}

// Requests to different resource paths (cluster/pool) must land in separate
// batches even if the machine template is identical.
func TestDifferentResourcePathsSeparateBatches(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
	g.Expect(key2).ToNot(gomega.Equal(key1), "different resource paths should produce different batch keys")

	// Execute each as separate batch (as the batcher would)
	exec.executeBatch(context.Background(), &batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]{Key: key1, Requests: []*batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{r1}})
	exec.executeBatch(context.Background(), &batcher.Batch[aksMachineCreatePayload, *offerings.HandlableError]{Key: key2, Requests: []*batcher.BatchedRequest[aksMachineCreatePayload, *offerings.HandlableError]{r2}})

	g.Expect(mock.count.Load()).To(gomega.Equal(int32(2)), "different resource paths → 2 API calls")

	calls := mock.snapshot()
	g.Expect(calls[0].machineName).To(gomega.Equal("m-1"))
	g.Expect(calls[1].machineName).To(gomega.Equal("m-2"))
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
	g := gomega.NewWithT(t)
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
	g.Expect(resps[0].Err).ToNot(gomega.HaveOccurred(), "m-1 should have no operational error")
	g.Expect(resps[0].Payload).To(gomega.BeNil(), "m-1 should have no API error")
	// m-2: per-machine API error (no operational error, APIError payload)
	g.Expect(resps[1].Err).ToNot(gomega.HaveOccurred(), "m-2 should have no operational error")
	g.Expect(resps[1].Payload).ToNot(gomega.BeNil(), "m-2 should have an API error")
	g.Expect(resps[1].Payload.Code).To(gomega.Equal("InvalidParameter"))
	// m-3: success
	g.Expect(resps[2].Err).ToNot(gomega.HaveOccurred(), "m-3 should have no operational error")
	g.Expect(resps[2].Payload).To(gomega.BeNil(), "m-3 should have no API error")
}

func TestExecutorBatchClientError_AllFail(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	mock := &recordingClient{err: makeBatchClientError([]testErrorDetail{
		{Code: "InvalidParameter", Message: "client error for m-1", Target: "m-1"},
		{Code: "InternalOperationError", Message: "internal error for m-2", Target: "m-2"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	resps := awaitAll(t, r1, r2)
	g.Expect(resps[0].Err).ToNot(gomega.HaveOccurred())
	g.Expect(resps[0].Payload).ToNot(gomega.BeNil())
	g.Expect(resps[0].Payload.Code).To(gomega.Equal("InvalidParameter"))
	g.Expect(resps[1].Err).ToNot(gomega.HaveOccurred())
	g.Expect(resps[1].Payload).ToNot(gomega.BeNil())
	g.Expect(resps[1].Payload.Code).To(gomega.Equal("InternalOperationError"))
}

func TestExecutorBatchInternalServerError_PartialFailure(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	mock := &recordingClient{err: makeBatchInternalServerError([]testErrorDetail{
		{Code: "InternalOperationError", Message: "internal error for m-1", Target: "m-1"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	resps := awaitAll(t, r1, r2)
	g.Expect(resps[0].Err).ToNot(gomega.HaveOccurred())
	g.Expect(resps[0].Payload).ToNot(gomega.BeNil(), "m-1 should have an API error detail")
	g.Expect(resps[0].Payload.Code).To(gomega.Equal("InternalOperationError"))
	g.Expect(resps[1].Err).ToNot(gomega.HaveOccurred(), "m-2 should have no operational error")
	g.Expect(resps[1].Payload).To(gomega.BeNil(), "m-2 should succeed")
}

func TestExecutorBatchInternalServerError_AllFail(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	mock := &recordingClient{err: makeBatchInternalServerError([]testErrorDetail{
		{Code: "InternalOperationError", Message: "error for m-1", Target: "m-1"},
		{Code: "InternalOperationError", Message: "error for m-2", Target: "m-2"},
	})}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	resps := awaitAll(t, r1, r2)
	g.Expect(resps[0].Err).ToNot(gomega.HaveOccurred())
	g.Expect(resps[0].Payload).ToNot(gomega.BeNil())
	g.Expect(resps[1].Err).ToNot(gomega.HaveOccurred())
	g.Expect(resps[1].Payload).ToNot(gomega.BeNil())
}

func TestExecutorNonBatchError_FallsBackToDistributeAll(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
	// Non-batch error (plain error) → extractPerMachineErrors fails → operational error
	mock := &recordingClient{err: fmt.Errorf("plain error")}
	exec := newExecutor(mock)

	r1 := makeReq("m-1", tpl("Standard_D2s_v3", []string{"1"}, nil))
	r2 := makeReq("m-2", tpl("Standard_D2s_v3", []string{"2"}, nil))
	exec.executeBatch(context.Background(), makeBatch(r1, r2))

	for _, resp := range awaitAll(t, r1, r2) {
		g.Expect(resp.Err).To(gomega.HaveOccurred())
		g.Expect(resp.Payload).To(gomega.BeNil())
	}
}

func TestExecutorUnknownBatchErrorCode_DistributesAsSingleAPIError(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
		g.Expect(resp.Err).ToNot(gomega.HaveOccurred())
		g.Expect(resp.Payload).ToNot(gomega.BeNil())
		g.Expect(resp.Payload.Code).To(gomega.Equal("SomeUnknownError"))
	}
}

func TestExecutorBatchErrorMalformedBody_DistributesAsOperationalError(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
		g.Expect(resp.Err).To(gomega.HaveOccurred())
		g.Expect(resp.Payload).To(gomega.BeNil())
	}
}

func TestExecutorBatchErrorNoRawResponse_DistributesAsOperationalError(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
		g.Expect(resp.Err).To(gomega.HaveOccurred())
		g.Expect(resp.Payload).To(gomega.BeNil())
	}
}
