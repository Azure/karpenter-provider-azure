// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

type fakeAKSMachineClienter struct {
	machines []*armcontainerservice.Machine
	nilPager bool
	err      error
}

func (f *fakeAKSMachineClienter) NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
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

func (f *fakeAKSMachineClienter) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error) {
	return armcontainerservice.MachinesClientGetResponse{}, nil
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
			lastUpdated: time.Now().Add(-60 * time.Second),
			nilPager:    true,
			expectError: true,
		},
		{
			name:        "pager returns error",
			lastUpdated: time.Now().Add(-60 * time.Second),
			pagerErr:    errors.New("pager error"),
			expectError: true,
		},
		{
			name:        "cache update with valid and invalid machines",
			lastUpdated: time.Now().Add(-60 * time.Second),
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
			fakePager := &fakeAKSMachineClienter{
				err:      tt.pagerErr,
				nilPager: tt.nilPager,
				machines: tt.returnedMachines,
			}

			cache := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  30 * time.Second,
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

				compareWithExpected(t, actualMachines, tt.expectedCache, "Update()")
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
			lastUpdated:     time.Now().Add(-60 * time.Second),
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
				ttl:                  30 * time.Second,
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
	}{
		{
			name:             "fresh cache with two items",
			lastUpdated:      time.Now(),
			cachedMachines:   twoMachines,
			expectedMachines: twoMachines,
			expectErr:        false,
		},
		{
			name:             "stale cache - expect error",
			lastUpdated:      time.Now().Add(-60 * time.Second),
			cachedMachines:   twoMachines,
			expectedMachines: nil,
			expectErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &MachineCache{
				lastUpdatedUnixNanos: atomic.Int64{},
				ttl:                  30 * time.Second,
			}

			for _, m := range tt.cachedMachines {
				c.machines.Store(lo.FromPtr(m.Name), m)
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())

			machines, err := c.List(context.Background())
			if (err != nil) != tt.expectErr {
				t.Errorf("List() error = %v, expectErr %v", err, tt.expectErr)
				return
			}

			compareWithExpected(t, machines, tt.expectedMachines, "List()")
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
			name: "stale cache times out",
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
			},
			lastUpdated:             time.Now().Add(-60 * time.Second),
			expectPollErr:           true,
			expectedProvisioningErr: nil,
		},
		{
			name: "nil properties times out",
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
				ttl:                  30 * time.Second,
				pollInterval:         time.Millisecond,
				updateRequests:       make(chan struct{}, 1),
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())
			c.machines.Store(lo.FromPtr(tt.machine.Name), tt.machine)

			ctx := context.Background()
			if tt.expectPollErr {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}

			provisioningErr, pollErr := c.PollUntilDone(ctx, *tt.machine.Name)
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
		expectProvisioningError bool
		expectPollerError       bool
		expectDone              bool
	}{
		{
			name:                    "creating state",
			provisioningState:       "Creating",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "updating state",
			provisioningState:       "Updating",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
		{
			name:                    "deleting state",
			provisioningState:       "Deleting",
			expectProvisioningError: false,
			expectPollerError:       true,
			expectDone:              true,
		},
		{
			name:                    "succeeded state",
			provisioningState:       "Succeeded",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              true,
		},
		{
			name:                    "failed state with provisioning error",
			provisioningState:       "Failed",
			expectProvisioningError: true,
			expectPollerError:       false,
			expectDone:              true,
		},
		{
			name:                    "unrecognized state continues polling",
			provisioningState:       "UnknownState",
			expectProvisioningError: false,
			expectPollerError:       false,
			expectDone:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &MachineCache{}

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

			provisioningErr, pollerErr, done := c.handleProvisioningState(ctx, machine, "test-machine")

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
			lastUpdated: time.Now().Add(-30 * time.Second),
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
				ttl:                  30 * time.Second,
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

func compareWithExpected(t *testing.T, got, expected []*armcontainerservice.Machine, testName string) {
	t.Helper()

	if len(got) != len(expected) {
		t.Errorf("%s cache size = %d, want %d", testName, len(got), len(expected))
	}

	for _, expectedMachine := range expected {
		found := false
		for _, actualMachine := range got {
			if lo.FromPtr(actualMachine.Name) == lo.FromPtr(expectedMachine.Name) {
				if pretty.Compare(actualMachine, expectedMachine) == "" {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("%s expected machine %q not found in cache", testName, lo.FromPtr(expectedMachine.Name))
		}
	}
}
