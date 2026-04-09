package machinecache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/kylelemons/godebug/pretty"
	"github.com/samber/lo"
)

type fakeAKSMachineNewListPager struct {
	machines []*armcontainerservice.Machine
	nilPager bool
	err      error
}

func (f *fakeAKSMachineNewListPager) NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	if f.nilPager {
		return nil
	}

	return runtime.NewPager(runtime.PagingHandler[armcontainerservice.MachinesClientListResponse]{
		More: func(page armcontainerservice.MachinesClientListResponse) bool {
			return false
		},
		Fetcher: func(ctx context.Context, page *armcontainerservice.MachinesClientListResponse) (armcontainerservice.MachinesClientListResponse, error) {
			return armcontainerservice.MachinesClientListResponse{
				MachineListResult: armcontainerservice.MachineListResult{
					Value: f.machines,
				},
			}, f.err
		},
	})
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		lastUpdated      time.Time
		nilPager         bool
		pagerErr         error
		existingCache    []*armcontainerservice.Machine
		returnedMachines []*armcontainerservice.Machine
		expectedCache    []*armcontainerservice.Machine
		expectError      bool
	}{
		{
			name:        "cache is fresh - no op",
			lastUpdated: time.Now(),
			expectError: false,
		},
		{
			name:        "nil pager - error",
			lastUpdated: time.Now().Add(-2 * DefaultMachineListCacheTTL),
			nilPager:    true,
			expectError: true,
		},
		{
			name:        "pager returns error",
			lastUpdated: time.Now().Add(-2 * DefaultMachineListCacheTTL),
			pagerErr:    errors.New("pager error"),
			expectError: true,
		},
		{
			name:        "cache update with valid and invalid machines",
			lastUpdated: time.Now().Add(-2 * DefaultMachineListCacheTTL),
			existingCache: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(provisioningStateCreating),
						Tags: map[string]*string{
							nodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
				{
					Name: to.Ptr("machine2"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(provisioningStateCreating),
						Tags: map[string]*string{
							nodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
			},
			returnedMachines: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(provisioningStateSucceeded),
						Tags: map[string]*string{
							nodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
				{
					Name: to.Ptr("machine3"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(provisioningStateSucceeded),
						Tags:              nil,
					},
				},
			},
			expectedCache: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(provisioningStateSucceeded),
						Tags: map[string]*string{
							nodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fakePager := &fakeAKSMachineNewListPager{
				err:      tt.pagerErr,
				nilPager: tt.nilPager,
				machines: tt.returnedMachines,
			}

			cache := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  DefaultMachineListCacheTTL,
				client:               fakePager,
				clusterResourceGroup: "test-rg",
				clusterName:          "test-cluster",
				aksMachinesPoolName:  "test-pool",
			}

			for _, m := range tt.existingCache {
				cache.machines.Store(lo.FromPtr(m.Name), m)
			}

			cache.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())
			err := cache.update(context.Background())
			if (err != nil) != tt.expectError {
				t.Errorf("Update() error = %v, expectError %v", err, tt.expectError)
			}

			if tt.expectedCache != nil {
				var actualMachines []*armcontainerservice.Machine
				cache.machines.Range(func(key, value any) bool {
					actualMachines = append(actualMachines, value.(*armcontainerservice.Machine))
					return true
				})

				if len(actualMachines) != len(tt.expectedCache) {
					t.Errorf("Update() cache size = %d, want %d", len(actualMachines), len(tt.expectedCache))
				}

				for _, expected := range tt.expectedCache {
					found := false
					for _, actual := range actualMachines {
						if lo.FromPtr(actual.Name) == lo.FromPtr(expected.Name) {
							if pretty.Compare(actual, expected) == "" {
								found = true
								break
							}
						}
					}
					if !found {
						t.Errorf("Update() expected machine %q not found in cache", lo.FromPtr(expected.Name))
					}
				}
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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

func TestList(t *testing.T) {
	t.Parallel()

	// Shared test data
	twoMachines := []*armcontainerservice.Machine{
		{
			Name: to.Ptr("machine1"),
			Properties: &armcontainerservice.MachineProperties{
				ProvisioningState: to.Ptr(provisioningStateSucceeded),
			},
		},
		{
			Name: to.Ptr("machine2"),
			Properties: &armcontainerservice.MachineProperties{
				ProvisioningState: to.Ptr(provisioningStateCreating),
			},
		},
	}

	tests := []struct {
		name             string
		lastUpdated      time.Time
		cachedMachines   []*armcontainerservice.Machine
		expectedMachines []*armcontainerservice.Machine
		expectErr        bool
		setupCache       func(*MachineCache) func() // returns cleanup function
	}{
		{
			name:             "fresh cache with two items",
			lastUpdated:      time.Now(),
			cachedMachines:   twoMachines,
			expectedMachines: twoMachines,
			expectErr:        false,
		},
		{
			name:             "stale cache refreshed by background worker",
			lastUpdated:      time.Now().Add(-2 * DefaultMachineListCacheTTL),
			cachedMachines:   twoMachines,
			expectedMachines: twoMachines,
			expectErr:        false,
			setupCache: func(c *MachineCache) func() {
				ctx, cancel := context.WithCancel(context.Background())
				c.updateRequests = make(chan struct{}, 1)
				c.workerCtx = ctx
				c.workerCancel = cancel

				// Background worker that refreshes cache
				go func() {
					select {
					case <-c.updateRequests:
						c.lastUpdatedUnixNanos.Store(time.Now().UnixNano())
					case <-ctx.Done():
						return
					}
				}()

				return cancel
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			t.Parallel()
			c := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  DefaultMachineListCacheTTL,
			}

			for _, m := range tt.cachedMachines {
				c.machines.Store(lo.FromPtr(m.Name), m)
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())

			var cleanup func()
			if tt.setupCache != nil {
				cleanup = tt.setupCache(c)
				defer cleanup()
			}

			machines, err := c.List(context.Background())
			if (err != nil) != tt.expectErr {
				t.Errorf("List() error = %v, expectErr %v", err, tt.expectErr)
				return
			}

			if len(machines) != len(tt.expectedMachines) {
				t.Errorf("List() returned %d machines, want %d", len(machines), len(tt.expectedMachines))
				return
			}

			for _, expected := range tt.expectedMachines {
				found := false
				for _, actual := range machines {
					if lo.FromPtr(actual.Name) == lo.FromPtr(expected.Name) {
						if pretty.Compare(actual, expected) != "" {
							t.Errorf("List() machine %q mismatch:\n%s", lo.FromPtr(expected.Name), pretty.Compare(actual, expected))
						}
						found = true
						break
					}
				}
				if !found {
					t.Errorf("List() expected machine %q not found in results", lo.FromPtr(expected.Name))
				}
			}
		})
	}
}

func TestPollUntilDone(t *testing.T) {
	t.Parallel()
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
			lastUpdated:   time.Now(),
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
			t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
		{
			name:        "uninitialized cache",
			lastUpdated: time.Time{},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
			if got := isValid(ctx, tt.properties, "test-nodepool"); got != tt.expected {
				t.Errorf("isValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}
