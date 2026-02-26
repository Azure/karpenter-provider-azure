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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/stretchr/testify/assert"
)

func TestComputeTemplateHash(t *testing.T) {
	t.Parallel()

	vmSize1 := "Standard_D2s_v3"
	vmSize2 := "Standard_D4s_v3"
	zone1 := "1"
	zone2 := "2"
	name1 := "machine1"
	name2 := "machine2"

	template1 := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize1,
			},
		},
		Zones: []*string{&zone1},
		Name:  &name1,
	}

	template2 := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize1,
			},
		},
		Zones: []*string{&zone2},
		Name:  &name2,
	}

	template3 := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{
				VMSize: &vmSize2,
			},
		},
		Zones: []*string{&zone1},
		Name:  &name1,
	}

	hash1 := computeTemplateHash(template1)
	hash2 := computeTemplateHash(template2)
	hash3 := computeTemplateHash(template3)

	assert.Equal(t, hash1, hash2, "hashes should be equal when only zones and names differ")
	assert.NotEqual(t, hash1, hash3, "hashes should differ when VM size differs")
}

func TestGrouperEnqueueCreate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	vmSize := "Standard_D2s_v3"
	zone := "1"
	machineName := "machine1"

	req := &CreateRequest{
		ctx:          ctx,
		machineName:  machineName,
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize,
				},
			},
			Zones: []*string{&zone},
		},
	}

	responseChan := grouper.EnqueueCreate(req)
	assert.NotNil(t, responseChan)

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 1, "should have one pending batch")
	grouper.mu.Unlock()
}

func TestGrouperBatchesSameTemplate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	vmSize := "Standard_D2s_v3"
	zone1 := "1"
	zone2 := "2"

	req1 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine1",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize,
				},
			},
			Zones: []*string{&zone1},
		},
	}

	req2 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine2",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize,
				},
			},
			Zones: []*string{&zone2},
		},
	}

	grouper.EnqueueCreate(req1)
	grouper.EnqueueCreate(req2)

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 1, "should have one pending batch for same template")
	for _, batch := range grouper.batches {
		assert.Len(t, batch.requests, 2, "batch should contain both requests")
	}
	grouper.mu.Unlock()
}

func TestGrouperBatchesDifferentTemplate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	vmSize1 := "Standard_D2s_v3"
	vmSize2 := "Standard_D4s_v3"
	zone := "1"

	req1 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine1",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize1,
				},
			},
			Zones: []*string{&zone},
		},
	}

	req2 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine2",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{
					VMSize: &vmSize2,
				},
			},
			Zones: []*string{&zone},
		},
	}

	grouper.EnqueueCreate(req1)
	grouper.EnqueueCreate(req2)

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 2, "should have two pending batches for different templates")
	grouper.mu.Unlock()
}
