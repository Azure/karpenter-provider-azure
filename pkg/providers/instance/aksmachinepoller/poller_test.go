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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/consts"
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
		PollInterval:  10 * time.Millisecond,
		RetryDelay:    5 * time.Millisecond,
		MaxRetryDelay: 20 * time.Millisecond,
		MaxRetries:    3,
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
	m := machineWithState(consts.ProvisioningStateFailed)
	m.Properties.Status = &armcontainerservice.MachineStatus{
		ProvisioningError: &armcontainerservice.ErrorDetail{
			Code:    lo.ToPtr(errorCode),
			Message: lo.ToPtr(errorMsg),
		},
	}
	return m
}

func TestPollUntilDone_ImmediateSuccess(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateSucceeded)},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).ToNot(HaveOccurred())
	g.Expect(mock.CallCount()).To(Equal(1))
}

func TestPollUntilDone_CreatingThenSucceeded(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{machine: machineWithState(consts.ProvisioningStateSucceeded)},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).ToNot(HaveOccurred())
	g.Expect(mock.CallCount()).To(Equal(3))
}

func TestPollUntilDone_CreatingThenFailed(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{machine: machineWithFailedState("SkuNotAvailable", "SKU not available in region")},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).ToNot(BeNil())
	g.Expect(lo.FromPtr(provisioningErr.Code)).To(Equal("SkuNotAvailable"))
	g.Expect(pollerErr).ToNot(HaveOccurred())
	g.Expect(mock.CallCount()).To(Equal(2))
}

func TestPollUntilDone_ImmediateFailed(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithFailedState("SkuNotAvailable", "SKU not available in region")},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).ToNot(BeNil())
	g.Expect(lo.FromPtr(provisioningErr.Code)).To(Equal("SkuNotAvailable"))
	g.Expect(pollerErr).ToNot(HaveOccurred())
	g.Expect(mock.CallCount()).To(Equal(1))
}

func TestPollUntilDone_FailedWithoutProvisioningError(t *testing.T) {
	g := NewWithT(t)
	m := machineWithState(consts.ProvisioningStateFailed)
	// No ProvisioningError set
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: m},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).To(MatchError(ContainSubstring("ProvisioningError is nil")))
}

func TestPollUntilDone_Deleting(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateDeleting)},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).To(MatchError(ContainSubstring("canceled provisioning state")))
}

func TestPollUntilDone_ContextCancelled(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateCreating)},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(ctx)

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).To(MatchError(ContainSubstring("context canceled")))
}

func TestPollUntilDone_TransientErrorRetry(t *testing.T) {
	g := NewWithT(t)
	transientErr := &azcore.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		ErrorCode:  "TooManyRequests",
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{err: transientErr}, // transient error
			{machine: machineWithState(consts.ProvisioningStateSucceeded)}, // retry succeeds
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).ToNot(HaveOccurred())
	g.Expect(mock.CallCount()).To(Equal(3))
}

func TestPollUntilDone_NonTransientErrorFails(t *testing.T) {
	g := NewWithT(t)
	notFoundErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "NotFound",
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{err: notFoundErr}, // not found
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).To(MatchError(ContainSubstring("non-retryable error")))
	g.Expect(mock.CallCount()).To(Equal(2))
}

func TestPollUntilDone_ExhaustedRetries(t *testing.T) {
	g := NewWithT(t)
	transientErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "InternalServerError",
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{err: transientErr}, // consumes retry 1
			{err: transientErr}, // retry 2
			{err: transientErr}, // retry 3
			{err: transientErr}, // retry exhausted
		},
	}

	opts := testOptions()
	opts.MaxRetries = 3

	poller := NewPoller(opts, mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).To(MatchError(ContainSubstring("exhausting")))
}

func TestPollUntilDone_NilProvisioningStateRetry(t *testing.T) {
	g := NewWithT(t)
	machineWithNilState := &armcontainerservice.Machine{
		ID:   lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/..."),
		Name: lo.ToPtr("machine"),
		Properties: &armcontainerservice.MachineProperties{
			ProvisioningState: nil, // nil state
		},
	}

	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithNilState},                                 // nil state, consumes retry
			{machine: machineWithNilState},                                 // nil state, consumes retry
			{machine: machineWithState(consts.ProvisioningStateSucceeded)}, // succeeds
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).ToNot(HaveOccurred())
}

func TestPollUntilDone_RetryBudgetResetsOnHealthyState(t *testing.T) {
	g := NewWithT(t)
	transientErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "InternalServerError",
	}

	// With MaxRetries=2:
	// 1. Creating (healthy - budget stays at 2)
	// 2. transient error (budget: 2→1, retries)
	// 3. transient error (budget: 1→0, retries)
	// 4. Creating (healthy - budget resets to 2)
	// 5. transient error (budget: 2→1, retries)
	// 6. transient error (budget: 1→0, retries)
	// 7. Succeeded (done)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{err: transientErr},
			{err: transientErr},
			{machine: machineWithState(consts.ProvisioningStateCreating)},
			{err: transientErr},
			{err: transientErr},
			{machine: machineWithState(consts.ProvisioningStateSucceeded)},
		},
	}

	opts := testOptions()
	opts.MaxRetries = 2

	poller := NewPoller(opts, mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).ToNot(HaveOccurred())
	g.Expect(mock.CallCount()).To(Equal(7))
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
			g := NewWithT(t)
			result := isTransientError(tt.err)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestPollUntilDone_UpdatingState(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState(consts.ProvisioningStateUpdating)},
			{machine: machineWithState(consts.ProvisioningStateUpdating)},
			{machine: machineWithState(consts.ProvisioningStateSucceeded)},
		},
	}

	poller := NewPoller(testOptions(), mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).ToNot(HaveOccurred())
}

func TestPollUntilDone_UnrecognizedStateExhaustsRetries(t *testing.T) {
	g := NewWithT(t)
	mock := &mockGetter{
		responses: []mockResponse{
			{machine: machineWithState("UnknownState")}, // consumes retry 1
			{machine: machineWithState("UnknownState")}, // retry 2
			{machine: machineWithState("UnknownState")}, // retry 3
			{machine: machineWithState("UnknownState")}, // retry exhausted
		},
	}

	opts := testOptions()
	opts.MaxRetries = 3

	poller := NewPoller(opts, mock, "rg", "cluster", "pool", "machine")
	provisioningErr, pollerErr := poller.PollUntilDone(context.Background())

	g.Expect(provisioningErr).To(BeNil())
	g.Expect(pollerErr).To(MatchError(ContainSubstring("unrecognized provisioning state")))
}
