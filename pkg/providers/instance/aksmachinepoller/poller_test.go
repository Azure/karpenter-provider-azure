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

package aksmachinepoller

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGetter implements AKSMachineGetter for testing
type mockGetter struct {
	responses []mockResponse
	callCount atomic.Int32
}

type mockResponse struct {
	machine *armcontainerservice.Machine
	err     error
}

func (m *mockGetter) Get(
	ctx context.Context,
	resourceGroupName string,
	resourceName string,
	agentPoolName string,
	aksMachineName string,
	options *armcontainerservice.MachinesClientGetOptions,
) (armcontainerservice.MachinesClientGetResponse, error) {
	idx := int(m.callCount.Add(1)) - 1
	if idx >= len(m.responses) {
		// Return last response if we've exhausted the list
		idx = len(m.responses) - 1
	}
	resp := m.responses[idx]
	if resp.err != nil {
		return armcontainerservice.MachinesClientGetResponse{}, resp.err
	}
	return armcontainerservice.MachinesClientGetResponse{
		Machine: *resp.machine,
	}, nil
}

func (m *mockGetter) CallCount() int {
	return int(m.callCount.Load())
}

func testOptions() Options {
	return Options{
		PollInterval:      10 * time.Millisecond,
		RetryDelay:    5 * time.Millisecond,
		MaxRetryDelay:     20 * time.Millisecond,
		MaxRetries:        3,
	}
}

func machineWithState(state string) *armcontainerservice.Machine {
	return &armcontainerservice.Machine{
		ID:   lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/agentPools/pool/machines/machine"),
		Name: lo.ToPtr("machine"),
		Properties: &armcontainerservice.MachineProperties{
			ProvisioningState: lo.ToPtr(state),
		},
	}
}

func machineWithFailedState(errorCode, errorMsg string) *armcontainerservice.Machine {
	m := machineWithState(ProvisioningStateFailed)
	m.Properties.Status = &armcontainerservice.MachineStatus{
		ProvisioningError: &armcontainerservice.ErrorDetail{
			Code:    lo.ToPtr(errorCode),
			Message: lo.ToPtr(errorMsg),
		},
	}
	return m
}

func TestPoller_PollUntilDone_ImmediateSuccess(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateSucceeded)},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.NoError(t, pollerErr)
	assert.Equal(t, 1, mock.CallCount(), "should complete with single GET due to immediate first poll")
}

func TestPoller_PollUntilDone_CreatingThenSucceeded(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateCreating)},  // immediate first poll
			{machine: machineWithState(ProvisioningStateCreating)},  // first tick
			{machine: machineWithState(ProvisioningStateSucceeded)}, // second tick
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.NoError(t, pollerErr)
	assert.Equal(t, 3, mock.CallCount())
}

func TestPoller_PollUntilDone_ImmediateFailed(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithFailedState("SkuNotAvailable", "SKU not available in region")},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	require.NotNil(t, provisioningErr)
	assert.Equal(t, "SkuNotAvailable", lo.FromPtr(provisioningErr.Code))
	assert.NoError(t, pollerErr)
	assert.Equal(t, 1, mock.CallCount())
}

func TestPoller_PollUntilDone_FailedWithoutProvisioningError(t *testing.T) {
	m := machineWithState(ProvisioningStateFailed)
	// No ProvisioningError set
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: m},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.Error(t, pollerErr)
	assert.Contains(t, pollerErr.Error(), "ProvisioningError is nil")
}

func TestPoller_PollUntilDone_Deleting(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateDeleting)},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.Error(t, pollerErr)
	assert.Contains(t, pollerErr.Error(), "canceled provisioning state")
}

func TestPoller_PollUntilDone_ContextCancelled(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateCreating)},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(ctx)

	assert.Nil(t, provisioningErr)
	assert.Error(t, pollerErr)
	assert.Contains(t, pollerErr.Error(), "context canceled")
}

func TestPoller_PollUntilDone_TransientErrorRetry(t *testing.T) {
	transientErr := &azcore.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		ErrorCode:  "TooManyRequests",
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateCreating)}, // immediate first poll
			{err: transientErr}, // first tick - transient error
			{machine: machineWithState(ProvisioningStateSucceeded)}, // retry succeeds
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.NoError(t, pollerErr)
	assert.Equal(t, 3, mock.CallCount())
}

func TestPoller_PollUntilDone_NonTransientErrorFails(t *testing.T) {
	notFoundErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "NotFound",
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateCreating)}, // immediate first poll
			{err: notFoundErr}, // first tick - not found
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.Error(t, pollerErr)
	assert.Contains(t, pollerErr.Error(), "non-retryable error")
	assert.Equal(t, 2, mock.CallCount())
}

func TestPoller_PollUntilDone_ExhaustedRetries(t *testing.T) {
	transientErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "InternalServerError",
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{err: transientErr}, // immediate first poll - consumes retry 1
			{err: transientErr}, // retry 2
			{err: transientErr}, // retry 3
			{err: transientErr}, // retry exhausted
		},
	}

	opts := testOptions()
	opts.MaxRetries = 3

	poller := NewPoller(opts, mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.Error(t, pollerErr)
	assert.Contains(t, pollerErr.Error(), "exhausting")
}

func TestPoller_PollUntilDone_NilProvisioningStateRetry(t *testing.T) {
	machineWithNilState := &armcontainerservice.Machine{
		ID:   lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/..."),
		Name: lo.ToPtr("machine"),
		Properties: &armcontainerservice.MachineProperties{
			ProvisioningState: nil, // nil state
		},
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithNilState},                          // immediate first poll - nil state, consumes retry
			{machine: machineWithNilState},                          // tick - nil state, consumes retry
			{machine: machineWithState(ProvisioningStateSucceeded)}, // tick succeeds
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.NoError(t, pollerErr)
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "408 RequestTimeout",
			err: &azcore.ResponseError{
				StatusCode: http.StatusRequestTimeout,
			},
			expected: true,
		},
		{
			name: "429 TooManyRequests",
			err: &azcore.ResponseError{
				StatusCode: http.StatusTooManyRequests,
			},
			expected: true,
		},
		{
			name: "500 InternalServerError",
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
			},
			expected: true,
		},
		{
			name: "502 BadGateway",
			err: &azcore.ResponseError{
				StatusCode: http.StatusBadGateway,
			},
			expected: true,
		},
		{
			name: "503 ServiceUnavailable",
			err: &azcore.ResponseError{
				StatusCode: http.StatusServiceUnavailable,
			},
			expected: true,
		},
		{
			name: "504 GatewayTimeout",
			err: &azcore.ResponseError{
				StatusCode: http.StatusGatewayTimeout,
			},
			expected: true,
		},
		{
			name: "404 NotFound - not transient",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
			},
			expected: false,
		},
		{
			name: "401 Unauthorized - not transient",
			err: &azcore.ResponseError{
				StatusCode: http.StatusUnauthorized,
			},
			expected: false,
		},
		{
			name: "403 Forbidden - not transient",
			err: &azcore.ResponseError{
				StatusCode: http.StatusForbidden,
			},
			expected: false,
		},
		{
			name:     "generic error - transient (network error)",
			err:      errors.New("connection reset by peer"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTransientError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPoller_PollUntilDone_UpdatingState(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(ProvisioningStateUpdating)},  // immediate first poll
			{machine: machineWithState(ProvisioningStateUpdating)},  // tick 1
			{machine: machineWithState(ProvisioningStateSucceeded)}, // tick 2
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.NoError(t, pollerErr)
}

func TestPoller_PollUntilDone_UnrecognizedStateExhaustsRetries(t *testing.T) {
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState("UnknownState")}, // immediate first poll - consumes retry 1
			{machine: machineWithState("UnknownState")}, // retry 2
			{machine: machineWithState("UnknownState")}, // retry 3
			{machine: machineWithState("UnknownState")}, // retry exhausted
		},
	}

	opts := testOptions()
	opts.MaxRetries = 3

	poller := NewPoller(opts, mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	assert.Nil(t, provisioningErr)
	assert.Error(t, pollerErr)
	assert.Contains(t, pollerErr.Error(), "unrecognized provisioning state")
}
