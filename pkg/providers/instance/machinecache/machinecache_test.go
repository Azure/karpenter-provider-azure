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
	"sort"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/Azure/karpenter-provider-azure/pkg/providers/launchtemplate"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

type fakeAKSMachineClienter struct {
	listRetval []*armcontainerservice.Machine
	nilPager   bool
	listErr    error

	getErr    error
	getRetval armcontainerservice.MachinesClientGetResponse
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
					Value: f.listRetval,
				},
			}, f.listErr
		},
	})
}

func (f *fakeAKSMachineClienter) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, machineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error) {
	return f.getRetval, f.getErr
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
			name:        "cache update - some machines removed",
			lastUpdated: time.Now().Add(-60 * time.Second),
			existingCache: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateCreating),
					},
				},
				{
					Name: to.Ptr("machine2"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateCreating),
					},
				},
			},
			returnedMachines: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
					},
				},
			},
			expectedCache: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
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
				listErr:    tt.pagerErr,
				nilPager:   tt.nilPager,
				listRetval: tt.returnedMachines,
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cache := NewMachineCache(
				ctx,
				fakePager,
				"test-rg",
				"test-cluster",
				"test-pool",
				WithTTL(30*time.Second),
				WithCacheDisabled(),
			)

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

func TestGetWithFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		lastUpdated     time.Time
		cachedMachines  []*armcontainerservice.Machine
		machineName     string
		getErr          error
		getRetval       armcontainerservice.MachinesClientGetResponse
		expectErr       bool
		expectedMachine *armcontainerservice.Machine
	}{
		{
			name:            "success - cache hit",
			lastUpdated:     time.Now(),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{&armcontainerservice.Machine{Name: to.Ptr("machine")}},
			expectErr:       false,
			expectedMachine: &armcontainerservice.Machine{Name: to.Ptr("machine")},
		},
		{
			name:            "cache miss - fallback to API",
			lastUpdated:     time.Now(),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{},
			getRetval:       armcontainerservice.MachinesClientGetResponse{Machine: armcontainerservice.Machine{Name: to.Ptr("machine")}},
			expectErr:       false,
			expectedMachine: &armcontainerservice.Machine{Name: to.Ptr("machine")},
		},
		{
			name:            "cache is stale - fallback to API",
			lastUpdated:     time.Now().Add(-60 * time.Second),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{&armcontainerservice.Machine{Name: to.Ptr("machine")}},
			getRetval:       armcontainerservice.MachinesClientGetResponse{Machine: armcontainerservice.Machine{Name: to.Ptr("machine")}},
			expectErr:       false,
			expectedMachine: &armcontainerservice.Machine{Name: to.Ptr("machine")},
		},
		{
			name:            "cache miss - API returns error",
			lastUpdated:     time.Now(),
			machineName:     "machine",
			cachedMachines:  []*armcontainerservice.Machine{},
			getErr:          errors.New("API error"),
			expectErr:       true,
			expectedMachine: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			c := MachineCache{
				client: &fakeAKSMachineClienter{
					getErr:    tt.getErr,
					getRetval: tt.getRetval,
				},
				clusterResourceGroup: "test-rg",
				clusterName:          "test-cluster",
				aksMachinesPoolName:  "test-pool",
				options:              defaultOpts(),
			}

			for _, m := range tt.cachedMachines {
				c.machines.Store(lo.FromPtr(m.Name), m)
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())

			machine, err := c.GetWithFallback(context.Background(), tt.machineName, true)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(machine).To(Equal(tt.expectedMachine))
		})
	}
}

func TestListWithFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		lastUpdated      time.Time
		cachedMachines   []*armcontainerservice.Machine
		listErr          error
		returnedMachines []*armcontainerservice.Machine
		expectErr        bool
		expectedMachines []*armcontainerservice.Machine
	}{
		{
			name:        "success - returns list from cache",
			lastUpdated: time.Now(),
			cachedMachines: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
						Tags: map[string]*string{
							launchtemplate.NodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
				{
					Name: to.Ptr("machine2"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
						Tags: map[string]*string{
							launchtemplate.NodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
			},
			expectErr: false,
			expectedMachines: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine1"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
						Tags: map[string]*string{
							launchtemplate.NodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
				{
					Name: to.Ptr("machine2"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
						Tags: map[string]*string{
							launchtemplate.NodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
			},
		},
		{
			name:        "success - falls back to API when cache is stale",
			lastUpdated: time.Now().Add(-60 * time.Second),
			returnedMachines: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine3"),
					Properties: &armcontainerservice.MachineProperties{
						Tags: map[string]*string{
							launchtemplate.NodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
			},
			expectErr: false,
			expectedMachines: []*armcontainerservice.Machine{
				{
					Name: to.Ptr("machine3"),
					Properties: &armcontainerservice.MachineProperties{
						Tags: map[string]*string{
							launchtemplate.NodePoolTagKey: to.Ptr("test-pool"),
						},
					},
				},
			},
		},
		{
			name:        "failure - API returns error",
			lastUpdated: time.Now().Add(-60 * time.Second),
			listErr:     errors.New("API error"),
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := &fakeAKSMachineClienter{
				listErr:    tt.listErr,
				listRetval: tt.returnedMachines,
			}

			c := MachineCache{
				client:               fakeClient,
				clusterResourceGroup: "test-rg",
				clusterName:          "test-cluster",
				aksMachinesPoolName:  "test-pool",
				options:              defaultOpts(),
			}

			for _, m := range tt.cachedMachines {
				c.machines.Store(lo.FromPtr(m.Name), m)
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())

			machines, err := c.ListWithFallback(context.Background(), true)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(machines).To(HaveLen(len(tt.expectedMachines)))

				// Sort both slices by name for deterministic comparison
				sortMachinesByName := func(m []*armcontainerservice.Machine) {
					sort.Slice(m, func(i, j int) bool {
						return lo.FromPtr(m[i].Name) < lo.FromPtr(m[j].Name)
					})
				}
				sortMachinesByName(machines)
				sortMachinesByName(tt.expectedMachines)

				for i, expected := range tt.expectedMachines {
					g.Expect(lo.FromPtr(machines[i].Name)).To(Equal(lo.FromPtr(expected.Name)))
				}
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
		{
			name:        "uninitialized cache",
			lastUpdated: time.Time{},
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
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			c := NewMachineCache(
				ctx,
				&fakeAKSMachineClienter{},
				"test-rg",
				"test-cluster",
				"test-pool",
				WithTTL(30*time.Second),
				WithCacheDisabled(),
			)
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())
			if got := c.isFresh(); got != tt.expected {
				t.Errorf("isFresh() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPollUntilDone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		getErr           error
		getRetval        armcontainerservice.MachinesClientGetResponse
		expectPollingErr bool
		expectErr        bool
		expectedDone     bool
	}{
		{
			name:             "CheckMachineExists fails - machine does not exist",
			getErr:           errors.New("Machine not found"),
			getRetval:        armcontainerservice.MachinesClientGetResponse{},
			expectErr:        true,
			expectPollingErr: false,
		},
		{
			name:   "PollOnce returns success state",
			getErr: nil,
			getRetval: armcontainerservice.MachinesClientGetResponse{
				Machine: armcontainerservice.Machine{
					Name: to.Ptr("machine"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
					},
				},
			},
			expectErr:        false,
			expectPollingErr: false,
		},
		{
			name:   "PollOnce returns failed state",
			getErr: nil,
			getRetval: armcontainerservice.MachinesClientGetResponse{
				Machine: armcontainerservice.Machine{
					Name: to.Ptr("machine"),
					Properties: &armcontainerservice.MachineProperties{
						ProvisioningState: to.Ptr(consts.ProvisioningStateFailed),
						Status: &armcontainerservice.MachineStatus{
							ProvisioningError: &armcontainerservice.ErrorDetail{
								Message: to.Ptr("Provisioning failed due to some error"),
							},
						},
					},
				},
			},
			expectErr:        false,
			expectPollingErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := &fakeAKSMachineClienter{
				getErr:    tt.getErr,
				getRetval: tt.getRetval,
			}

			c := MachineCache{
				client:  fakeClient,
				options: defaultOpts(),
			}
			c.options.pollInterval = time.Millisecond
			c.lastUpdatedUnixNanos.Store(time.Now().UnixNano())

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			provisioningErr, err := c.PollUntilDone(ctx, "machine")

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tt.expectPollingErr {
				g.Expect(provisioningErr).ToNot(BeNil())
			} else {
				g.Expect(provisioningErr).To(BeNil())
			}
		})
	}
}

func TestPollOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                    string
		machine                 *armcontainerservice.Machine
		lastUpdated             time.Time
		expectedProvisioningErr *armcontainerservice.ErrorDetail
		expectedPollerErr       bool
		expectedDone            bool
	}{
		{
			name:        "cache not fresh - returns not done",
			lastUpdated: time.Now().Add(-60 * time.Second),
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
			},
			expectedProvisioningErr: nil,
			expectedPollerErr:       false,
			expectedDone:            false,
		},
		{
			name:                    "machine not found in cache - returns error and done",
			lastUpdated:             time.Now(),
			machine:                 nil,
			expectedProvisioningErr: nil,
			expectedPollerErr:       true,
			expectedDone:            true,
		},
		{
			name:        "nil properties - returns not done",
			lastUpdated: time.Now(),
			machine: &armcontainerservice.Machine{
				Name:       to.Ptr("machine"),
				ID:         to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster/machines/machine"),
				Properties: nil,
			},
			expectedProvisioningErr: nil,
			expectedPollerErr:       false,
			expectedDone:            false,
		},
		{
			name:        "succeeded state - returns done",
			lastUpdated: time.Now(),
			machine: &armcontainerservice.Machine{
				Name: to.Ptr("machine"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: to.Ptr(consts.ProvisioningStateSucceeded),
				},
			},
			expectedProvisioningErr: nil,
			expectedPollerErr:       false,
			expectedDone:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			ctx := context.Background()

			c := MachineCache{
				options: defaultOpts(),
			}
			c.lastUpdatedUnixNanos.Store(tt.lastUpdated.UnixNano())

			if tt.machine != nil {
				c.machines.Store(lo.FromPtr(tt.machine.Name), tt.machine)
			}

			machineName := "machine"
			provisioningErr, pollerErr, done := c.pollOnce(ctx, machineName)

			g.Expect(provisioningErr).To(Equal(tt.expectedProvisioningErr))
			if tt.expectedPollerErr {
				g.Expect(pollerErr).To(HaveOccurred())
			} else {
				g.Expect(pollerErr).ToNot(HaveOccurred())
			}
			g.Expect(done).To(Equal(tt.expectedDone))
		})
	}
}

func TestIsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		properties *armcontainerservice.MachineProperties
		expected   bool
	}{
		{
			name: "valid properties",
			properties: &armcontainerservice.MachineProperties{
				Tags: map[string]*string{
					launchtemplate.NodePoolTagKey: lo.ToPtr("test-nodepool"),
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
			if got := isValid(tt.properties); got != tt.expected {
				t.Errorf("isValid() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func compareWithExpected(t *testing.T, got, expected []*armcontainerservice.Machine, testName string) {
	t.Helper()
	g := NewWithT(t)

	g.Expect(got).To(HaveLen(len(expected)), "%s cache size mismatch", testName)

	for _, expectedMachine := range expected {
		found := false
		for _, actualMachine := range got {
			if lo.FromPtr(actualMachine.Name) == lo.FromPtr(expectedMachine.Name) {
				success, _ := Equal(actualMachine).Match(expectedMachine)
				if success {
					found = true
					break
				}
			}
		}
		g.Expect(found).To(BeTrue(), "%s expected machine %q not found in cache", testName, lo.FromPtr(expectedMachine.Name))
	}
}
