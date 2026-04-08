package machinecache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/kylelemons/godebug/pretty"
	"github.com/samber/lo"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name            string
		lastUpdated     time.Time
		cachedMachines  []*armcontainerservice.Machine
		machineName     string
		expectErr       bool
		expectedMachine *armcontainerservice.Machine
	}{
		{
			name:            "success",
			lastUpdated:     time.Now(),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{&armcontainerservice.Machine{Name: to.Ptr("machine")}},
			expectErr:       false,
			expectedMachine: &armcontainerservice.Machine{Name: to.Ptr("machine")},
		},
		{
			name:            "machine not found",
			lastUpdated:     time.Now(),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{},
			expectErr:       true,
			expectedMachine: nil,
		},
		{
			name:            "stale cache",
			lastUpdated:     time.Now().Add(-2 * DefaultMachineListCacheTTL),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{&armcontainerservice.Machine{Name: to.Ptr("machine")}},
			expectErr:       true,
			expectedMachine: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  DefaultMachineListCacheTTL,
			}

			for _, m := range tt.cachedMachines {
				c.machines.Store(lo.FromPtr(m.Name), m)
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())

			machine, err := c.Get(tt.machineName)
			if (err != nil) != tt.expectErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.expectErr)
				return
			}
			if pretty.Compare(machine, tt.expectedMachine) != "" {
				t.Errorf("Get() = %v, want %v", machine, tt.expectedMachine)
			}
		})
	}
}

func TestPollUntilDone(t *testing.T) {
	tests := []struct {
		name                    string
		machine                 *armcontainerservice.Machine
		lastUpdated             time.Time
		expectPollErr           bool
		expectedProvisioningErr *armcontainerservice.ErrorDetail
	}{
		{
			name: "success",
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: to.Ptr(provisioningStateSucceeded),
				},
			},
			lastUpdated:             time.Now(),
			expectPollErr:           false,
			expectedProvisioningErr: nil,
		},
		{
			name: "provisioning error",
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: to.Ptr(provisioningStateFailed),
					Status: &armcontainerservice.MachineStatus{
						ProvisioningError: &armcontainerservice.ErrorDetail{
							Code:    to.Ptr("ProvisioningFailed"),
							Message: to.Ptr("Provisioning failed due to an error"),
						},
					},
				},
			},
			lastUpdated: time.Now(),
			expectPollErr: false,
			expectedProvisioningErr: &armcontainerservice.ErrorDetail{
				Code:    to.Ptr("ProvisioningFailed"),
				Message: to.Ptr("Provisioning failed due to an error"),
			},
		},
		{
			name: "polling error",
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: to.Ptr(provisioningStateDeleting),
				},
			},
			lastUpdated:             time.Now(),
			expectPollErr:           true,
			expectedProvisioningErr: nil,
		},
		{
			name: "stale cache no retries",
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
			},
			lastUpdated:             time.Now().Add(-2 * DefaultMachineListCacheTTL),
			expectPollErr:           true,
			expectedProvisioningErr: nil,
		},
		{
			name: "nil properties no retries",
			machine: &armcontainerservice.Machine{
				Name:       to.Ptr("machine"),
				ID:         to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/machines/machine"),
				Properties: nil,
			},
			lastUpdated:             time.Now(),
			expectPollErr:           true,
			expectedProvisioningErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  DefaultMachineListCacheTTL,
				interval:             time.Millisecond,
				maxRetries:           0,
				retryDelay:           time.Millisecond,
				maxRetryDelay:        time.Millisecond,
				updateRequests:       make(chan struct{}, 1),
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())
			c.machines.Store(lo.FromPtr(tt.machine.Name), tt.machine)

			provisioningErr, pollErr := c.PollUntilDone(context.Background(), *tt.machine.Name)
			if pretty.Compare(provisioningErr, tt.expectedProvisioningErr) != "" {
				t.Errorf("PollUntilDone() provisioningErr = %v, want %v", provisioningErr, tt.expectedProvisioningErr)
			}
			if (pollErr != nil) != tt.expectPollErr {
				t.Errorf("PollUntilDone() pollErr = %v, expectPollErr %v", pollErr, tt.expectPollErr)
			}
		})
	}
}

func TestHandleProvisioningState(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name                    string
		provisioningState       string
		retryAttemptsLeft       int
		expectProvisioningError bool
		expectPollerError       bool
		expectDone              bool
	}{
		{
			name:                    "creating state",
			provisioningState:       "Creating",
			retryAttemptsLeft:       1,
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "updating state",
			provisioningState:       "Updating",
			retryAttemptsLeft:       1,
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "deleting state",
			provisioningState:       "Deleting",
			retryAttemptsLeft:       1,
			expectProvisioningError: false,
			expectPollerError:       true,
			expectDone:              true,
		},
		{
			name:                    "succeeded state",
			provisioningState:       "Succeeded",
			retryAttemptsLeft:       1,
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              true,
		},
		{
			name:                    "failed state with provisioning error",
			provisioningState:       "Failed",
			retryAttemptsLeft:       1,
			expectProvisioningError: true,
			expectPollerError:       false,
			expectDone:              true,
		},
		{
			name:                    "unrecognized state with retry",
			provisioningState:       "UnknownState",
			retryAttemptsLeft:       1,
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "unrecognized state without retry",
			provisioningState:       "UnknownState",
			retryAttemptsLeft:       0,
			expectProvisioningError: false,
			expectPollerError:       true,
			expectDone:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MachineCache{
				retryDelay:    time.Millisecond,
				maxRetryDelay: time.Millisecond,
				maxRetries:    1,
			}

			machine := &armcontainerservice.Machine{
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: to.Ptr(tt.provisioningState),
				},
			}

			if tt.provisioningState == "Failed" {
				machine.Properties.Status = &armcontainerservice.MachineStatus{
					ProvisioningError: &armcontainerservice.ErrorDetail{
						Code:    to.Ptr("TestError"),
						Message: to.Ptr("Test error message"),
					},
				}
			}

			retryAttemptsLeft := tt.retryAttemptsLeft
			currentRetryDelay := time.Millisecond

			provisioningErr, pollerErr, done := c.handleProvisioningState(ctx, machine, "test-machine", &retryAttemptsLeft, &currentRetryDelay)

			if (provisioningErr != nil) != tt.expectProvisioningError {
				t.Errorf("handleProvisioningState() provisioningErr = %v, expectProvisioningError %v", provisioningErr, tt.expectProvisioningError)
			}
			if (pollerErr != nil) != tt.expectPollerError {
				t.Errorf("handleProvisioningState() pollerErr = %v, expectPollerError %v", pollerErr, tt.expectPollerError)
			}
			if done != tt.expectDone {
				t.Errorf("handleProvisioningState() done = %v, expectDone %v", done, tt.expectDone)
			}
		})
	}
}

func TestHandleNilProvisioningState(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name                    string
		retryAttemptsLeft       int
		expectProvisioningError bool
		expectPollerError       bool
		expectDone              bool
	}{
		{
			name:                    "retry with attempts remaining",
			retryAttemptsLeft:       1,
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "no retry without attempts",
			retryAttemptsLeft:       0,
			expectProvisioningError: false,
			expectPollerError:       true,
			expectDone:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MachineCache{
				retryDelay:    time.Millisecond,
				maxRetryDelay: time.Millisecond,
				maxRetries:    1,
			}

			machine := &armcontainerservice.Machine{
				ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/machines/test-machine"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: nil,
				},
			}

			retryAttemptsLeft := tt.retryAttemptsLeft
			currentRetryDelay := time.Millisecond

			provisioningErr, pollerErr, done := c.handleNilProvisioningState(ctx, machine, "test-machine", &retryAttemptsLeft, &currentRetryDelay)

			if (provisioningErr != nil) != tt.expectProvisioningError {
				t.Errorf("handleNilProvisioningState() provisioningErr = %v, expectProvisioningError %v", provisioningErr, tt.expectProvisioningError)
			}
			if (pollerErr != nil) != tt.expectPollerError {
				t.Errorf("handleNilProvisioningState() pollerErr = %v, expectPollerError %v", pollerErr, tt.expectPollerError)
			}
			if done != tt.expectDone {
				t.Errorf("handleNilProvisioningState() done = %v, expectDone %v", done, tt.expectDone)
			}
		})
	}
}

func TestIsFresh(t *testing.T) {
	tests := []struct {
		name        string
		lastUpdated time.Time
		expected    bool
	}{
		{
			name:        "fresh cache",
			lastUpdated: time.Now(),
			expected:    true,
		},
		{
			name:        "stale cache",
			lastUpdated: time.Now().Add(-1 * DefaultMachineListCacheTTL),
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  DefaultMachineListCacheTTL,
			}
			cache.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())
			if got := cache.isFresh(); got != tt.expected {
				t.Errorf("isFresh() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsValid(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		properties *armcontainerservice.MachineProperties
		expected   bool
	}{
		{
			name: "valid properties",
			properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{
					nodePoolTagKey: lo.ToPtr("test-nodepool"),
				},
			},
			expected: true,
		},
		{
			name:       "nil properties",
			properties: nil,
			expected:   false,
		},
		{
			name:       "empty properties",
			properties: &armcontainerservice.MachineProperties{},
			expected:   false,
		},
		{
			name: "missing node pool tag",
			properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{
					"someOtherTag": lo.ToPtr("value"),
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValid(ctx, tt.properties, "test-nodepool"); got != tt.expected {
				t.Errorf("isValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}
