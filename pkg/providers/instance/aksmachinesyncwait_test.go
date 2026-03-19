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

package instance

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/skewer"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1beta1"
	"github.com/Azure/karpenter-provider-azure/pkg/cache"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/instance/offerings"

	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// stubInstanceTypeProvider implements instancetype.Provider for tests.
type stubInstanceTypeProvider struct{}

func (s *stubInstanceTypeProvider) LivenessProbe(_ *http.Request) error { return nil }
func (s *stubInstanceTypeProvider) List(_ context.Context, _ *v1beta1.AKSNodeClass) ([]*corecloudprovider.InstanceType, error) {
	return nil, nil
}
func (s *stubInstanceTypeProvider) Get(_ context.Context, _ *v1beta1.AKSNodeClass, _ string) (*skewer.SKU, error) {
	// Return a minimal SKU — enough for handleMachineProvisioningError to proceed
	return &skewer.SKU{Name: lo.ToPtr("Standard_D2_v2")}, nil
}

// mockMachineGetter is a test helper that returns machines from a sequence of pre-configured responses.
// Each call increments the call counter and returns the next response.
type mockMachineGetter struct {
	responses []mockGetMachineResponse
	callCount atomic.Int32
}

type mockGetMachineResponse struct {
	machine *armcontainerservice.Machine
	err     error
}

func (m *mockMachineGetter) getMachine(_ context.Context, _ string) (*armcontainerservice.Machine, error) {
	idx := int(m.callCount.Add(1)) - 1
	if idx >= len(m.responses) {
		// Return the last response for any extra calls
		idx = len(m.responses) - 1
	}
	return m.responses[idx].machine, m.responses[idx].err
}

// makeMachine creates a minimal Machine with the given provisioning state and optional error.
func makeMachine(provisioningState string, errorCode string, errorMessage string) *armcontainerservice.Machine {
	machine := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			ProvisioningState: lo.ToPtr(provisioningState),
		},
	}
	if errorCode != "" || errorMessage != "" {
		machine.Properties.Status = &armcontainerservice.MachineStatus{
			ProvisioningError: &armcontainerservice.ErrorDetail{
				Code:    lo.ToPtr(errorCode),
				Message: lo.ToPtr(errorMessage),
			},
		}
	}
	return machine
}

func newTestProvider(timeout, interval time.Duration) *DefaultAKSMachineProvider {
	return &DefaultAKSMachineProvider{
		machineSyncWaitTimeout:  timeout,
		machineSyncPollInterval: interval,
		errorHandling:           offerings.NewErrorDetailHandler(cache.NewUnavailableOfferings()),
		instanceTypeProvider:    &stubInstanceTypeProvider{},
	}
}

// testInstanceType returns a minimal instance type for use in tests that trigger handleMachineProvisioningError.
func testInstanceType() *corecloudprovider.InstanceType {
	return &corecloudprovider.InstanceType{
		Name: "Standard_D2_v2",
	}
}

func TestSyncWaitForMachineStatus_FailedOnFirstPoll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := newTestProvider(10*time.Second, 10*time.Millisecond)

	failedMachine := makeMachine(consts.ProvisioningStateFailed, "OperationNotAllowed", "Quota exceeded")

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: failedMachine, err: nil},
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	_, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err == nil {
		t.Fatal("expected error when machine is Failed, got nil")
	}
	if mock.callCount.Load() != 1 {
		t.Fatalf("expected 1 poll call, got %d", mock.callCount.Load())
	}
	// Verify it contains the error info
	errStr := err.Error()
	if !strings.Contains(errStr, "sync wait") {
		t.Fatalf("error should reference sync wait phase, got: %s", errStr)
	}
}

func TestSyncWaitForMachineStatus_SucceededOnSecondPoll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := newTestProvider(10*time.Second, 10*time.Millisecond)

	creatingMachine := makeMachine(consts.ProvisioningStateCreating, "", "")
	succeededMachine := makeMachine(consts.ProvisioningStateSucceeded, "", "")

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: creatingMachine, err: nil},
			{machine: succeededMachine, err: nil},
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	result, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lo.FromPtr(result.Properties.ProvisioningState) != consts.ProvisioningStateSucceeded {
		t.Fatalf("expected Succeeded, got %s", lo.FromPtr(result.Properties.ProvisioningState))
	}
	if mock.callCount.Load() != 2 {
		t.Fatalf("expected 2 poll calls, got %d", mock.callCount.Load())
	}
}

func TestSyncWaitForMachineStatus_TimeoutWhileCreating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Very short timeout — should timeout after a few polls
	provider := newTestProvider(50*time.Millisecond, 10*time.Millisecond)

	creatingMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	result, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err != nil {
		t.Fatalf("expected no error on timeout (should hand off to LRO), got: %v", err)
	}
	if lo.FromPtr(result.Properties.ProvisioningState) != consts.ProvisioningStateCreating {
		t.Fatalf("expected Creating (timeout), got %s", lo.FromPtr(result.Properties.ProvisioningState))
	}
	// Should have polled at least once
	if mock.callCount.Load() < 1 {
		t.Fatal("expected at least 1 poll call during timeout")
	}
}

func TestSyncWaitForMachineStatus_DisabledWithZeroTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Zero timeout means disabled
	provider := newTestProvider(0, 10*time.Millisecond)

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: makeMachine(consts.ProvisioningStateFailed, "SomeError", "msg"), err: nil},
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	// With zero timeout, the loop condition time.Now().Before(deadline) is immediately false
	result, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err != nil {
		t.Fatalf("expected no error with zero timeout, got: %v", err)
	}
	// Should return the initial machine without polling
	if lo.FromPtr(result.Properties.ProvisioningState) != consts.ProvisioningStateCreating {
		t.Fatalf("expected Creating (zero timeout), got %s", lo.FromPtr(result.Properties.ProvisioningState))
	}
	if mock.callCount.Load() != 0 {
		t.Fatalf("expected 0 poll calls with zero timeout, got %d", mock.callCount.Load())
	}
}

func TestSyncWaitForMachineStatus_GetErrorsDuringPolling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := newTestProvider(10*time.Second, 10*time.Millisecond)

	succeededMachine := makeMachine(consts.ProvisioningStateSucceeded, "", "")

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: nil, err: fmt.Errorf("transient GET error")},
			{machine: nil, err: fmt.Errorf("another transient GET error")},
			{machine: succeededMachine, err: nil}, // recovers on 3rd poll
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	result, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lo.FromPtr(result.Properties.ProvisioningState) != consts.ProvisioningStateSucceeded {
		t.Fatalf("expected Succeeded after GET errors, got %s", lo.FromPtr(result.Properties.ProvisioningState))
	}
	if mock.callCount.Load() != 3 {
		t.Fatalf("expected 3 poll calls, got %d", mock.callCount.Load())
	}
}

func TestSyncWaitForMachineStatus_FailedWithNilProvisioningError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := newTestProvider(10*time.Second, 10*time.Millisecond)

	// Failed but no ProvisioningError — should still return an error
	failedMachineNoError := makeMachine(consts.ProvisioningStateFailed, "", "")
	failedMachineNoError.Properties.Status = nil // no status at all

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: failedMachineNoError, err: nil},
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	_, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err == nil {
		t.Fatal("expected error when machine is Failed with nil ProvisioningError")
	}
	if !strings.Contains(err.Error(), "Failed state") || !strings.Contains(err.Error(), "ProvisioningError is nil") {
		t.Fatalf("expected error about nil ProvisioningError, got: %s", err.Error())
	}
}

func TestSyncWaitForMachineStatus_ContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	provider := newTestProvider(10*time.Second, 50*time.Millisecond) // long timeout but we'll cancel

	creatingMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	// Cancel context quickly
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err != nil {
		t.Fatalf("expected no error on context cancellation, got: %v", err)
	}
	// Should return the last known machine state
	if result == nil {
		t.Fatal("expected non-nil result on context cancellation")
	}
}

func TestSyncWaitForMachineStatus_FailedAfterSeveralCreatingPolls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := newTestProvider(10*time.Second, 10*time.Millisecond)

	creatingMachine := makeMachine(consts.ProvisioningStateCreating, "", "")
	failedMachine := makeMachine(consts.ProvisioningStateFailed, "SubscriptionQuotaReached", "subscription quota reached for Standard_D2_v2")

	mock := &mockMachineGetter{
		responses: []mockGetMachineResponse{
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: creatingMachine, err: nil},
			{machine: failedMachine, err: nil}, // fails on 4th poll
		},
	}
	provider.getMachineFunc = mock.getMachine

	initialMachine := makeMachine(consts.ProvisioningStateCreating, "", "")

	_, err := provider.syncWaitForMachineStatus(ctx, "test-machine", nil, testInstanceType(), "", "", initialMachine)
	if err == nil {
		t.Fatal("expected error when machine transitions to Failed")
	}
	if mock.callCount.Load() != 4 {
		t.Fatalf("expected 4 poll calls, got %d", mock.callCount.Load())
	}
}
