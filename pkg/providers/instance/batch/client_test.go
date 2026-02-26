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

package batch

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockAKSMachinesClient struct {
	mock.Mock
}

func (m *MockAKSMachinesClient) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, parameters armcontainerservice.Machine, options *armcontainerservice.MachinesClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse], error) {
	args := m.Called(ctx, resourceGroupName, resourceName, agentPoolName, aksMachineName, parameters, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse]), args.Error(1)
}

func (m *MockAKSMachinesClient) Get(ctx context.Context, resourceGroupName string, resourceName string, agentPoolName string, aksMachineName string, options *armcontainerservice.MachinesClientGetOptions) (armcontainerservice.MachinesClientGetResponse, error) {
	args := m.Called(ctx, resourceGroupName, resourceName, agentPoolName, aksMachineName, options)
	return args.Get(0).(armcontainerservice.MachinesClientGetResponse), args.Error(1)
}

func (m *MockAKSMachinesClient) NewListPager(resourceGroupName string, resourceName string, agentPoolName string, options *armcontainerservice.MachinesClientListOptions) *runtime.Pager[armcontainerservice.MachinesClientListResponse] {
	args := m.Called(resourceGroupName, resourceName, agentPoolName, options)
	return args.Get(0).(*runtime.Pager[armcontainerservice.MachinesClientListResponse])
}

func TestBatchingClientPassthrough(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockClient := new(MockAKSMachinesClient)

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})
	grouper.enabled = false

	batchClient := NewBatchingMachinesClient(mockClient, grouper, "rg", "cluster", "pool")

	vmSize := "Standard_D2s_v3"
	template := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize,
			},
		},
	}

	mockClient.On("BeginCreateOrUpdate", ctx, "rg", "cluster", "pool", "machine1", template, (*armcontainerservice.MachinesClientBeginCreateOrUpdateOptions)(nil)).Return((*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse])(nil), nil)

	_, err := batchClient.BeginCreateOrUpdate(ctx, "rg", "cluster", "pool", "machine1", template, nil)
	assert.NoError(t, err)

	mockClient.AssertExpectations(t)
}

func TestBatchingClientSkipsUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockClient := new(MockAKSMachinesClient)

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})
	grouper.enabled = true

	batchClient := NewBatchingMachinesClient(mockClient, grouper, "rg", "cluster", "pool")

	vmSize := "Standard_D2s_v3"
	template := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize,
			},
		},
	}

	etag := "etag123"
	options := &armcontainerservice.MachinesClientBeginCreateOrUpdateOptions{
		IfMatch: &etag,
	}

	mockClient.On("BeginCreateOrUpdate", ctx, "rg", "cluster", "pool", "machine1", template, options).Return((*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse])(nil), nil)

	_, err := batchClient.BeginCreateOrUpdate(ctx, "rg", "cluster", "pool", "machine1", template, options)
	assert.NoError(t, err)

	mockClient.AssertExpectations(t)
}

func TestBatchingClientSkipsWhenContextFlagged(t *testing.T) {
	t.Parallel()

	ctx := WithSkipBatching(context.Background())
	mockClient := new(MockAKSMachinesClient)

	grouper := NewGrouper(context.Background(), Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})
	grouper.enabled = true

	batchClient := NewBatchingMachinesClient(mockClient, grouper, "rg", "cluster", "pool")

	vmSize := "Standard_D2s_v3"
	template := armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize,
			},
		},
	}

	mockClient.On("BeginCreateOrUpdate", ctx, "rg", "cluster", "pool", "machine1", template, (*armcontainerservice.MachinesClientBeginCreateOrUpdateOptions)(nil)).Return((*runtime.Poller[armcontainerservice.MachinesClientCreateOrUpdateResponse])(nil), nil)

	_, err := batchClient.BeginCreateOrUpdate(ctx, "rg", "cluster", "pool", "machine1", template, nil)
	assert.NoError(t, err)

	mockClient.AssertExpectations(t)
}
