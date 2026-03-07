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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestMachineListCache_GetOnEmptyCache(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	machine, ok := c.get("machine-1")
	assert.False(t, ok, "expected cache miss on empty cache")
	assert.Nil(t, machine)
}

func TestMachineListCache_GetAfterUpdate(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	machines := []*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-1"), Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D2_v2")},
		}},
		{Name: lo.ToPtr("machine-2"), Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: lo.ToPtr("Standard_D4_v2")},
		}},
	}

	c.update(machines)

	m1, ok := c.get("machine-1")
	assert.True(t, ok, "expected cache hit for machine-1")
	assert.Equal(t, "Standard_D2_v2", *m1.Properties.Hardware.VMSize)

	m2, ok := c.get("machine-2")
	assert.True(t, ok, "expected cache hit for machine-2")
	assert.Equal(t, "Standard_D4_v2", *m2.Properties.Hardware.VMSize)

	_, ok = c.get("machine-3")
	assert.False(t, ok, "expected cache miss for nonexistent machine-3")
}

func TestMachineListCache_TTLExpiry(t *testing.T) {
	// Use a very short TTL so we can test expiry
	c := newMachineListCache(1 * time.Millisecond)

	machines := []*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-1")},
	}

	c.update(machines)

	// Immediately should be fresh
	_, ok := c.get("machine-1")
	assert.True(t, ok, "expected cache hit immediately after update")

	// Wait for TTL to expire
	time.Sleep(5 * time.Millisecond)

	_, ok = c.get("machine-1")
	assert.False(t, ok, "expected cache miss after TTL expiry")
}

func TestMachineListCache_Invalidate(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	machines := []*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-1")},
		{Name: lo.ToPtr("machine-2")},
	}

	c.update(machines)

	// Invalidate machine-1
	c.invalidate("machine-1")

	_, ok := c.get("machine-1")
	assert.False(t, ok, "expected cache miss for invalidated machine-1")

	// machine-2 should still be cached
	_, ok = c.get("machine-2")
	assert.True(t, ok, "expected cache hit for machine-2 (not invalidated)")
}

func TestMachineListCache_InvalidateAll(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	machines := []*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-1")},
		{Name: lo.ToPtr("machine-2")},
	}

	c.update(machines)

	c.invalidateAll()

	_, ok := c.get("machine-1")
	assert.False(t, ok, "expected cache miss after invalidateAll")

	_, ok = c.get("machine-2")
	assert.False(t, ok, "expected cache miss after invalidateAll")
}

func TestMachineListCache_UpdateReplacesOldEntries(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	// First update with machine-1 and machine-2
	c.update([]*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-1")},
		{Name: lo.ToPtr("machine-2")},
	})

	// Second update with only machine-3 — should replace the whole cache
	c.update([]*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-3")},
	})

	_, ok := c.get("machine-1")
	assert.False(t, ok, "expected cache miss for machine-1 after replacement update")

	_, ok = c.get("machine-2")
	assert.False(t, ok, "expected cache miss for machine-2 after replacement update")

	_, ok = c.get("machine-3")
	assert.True(t, ok, "expected cache hit for machine-3 after replacement update")
}

func TestMachineListCache_NilNameSkipped(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	machines := []*armcontainerservice.Machine{
		{Name: nil}, // nil name should be skipped
		{Name: lo.ToPtr("machine-1")},
	}

	c.update(machines)

	// Should still have machine-1
	_, ok := c.get("machine-1")
	assert.True(t, ok, "expected cache hit for machine-1")

	// Cache should have exactly 1 entry
	c.mu.RLock()
	assert.Equal(t, 1, len(c.machines), "expected 1 entry in cache (nil name skipped)")
	c.mu.RUnlock()
}

func TestMachineListCache_ZeroTTLDisablesCache(t *testing.T) {
	c := newMachineListCache(0)

	c.update([]*armcontainerservice.Machine{
		{Name: lo.ToPtr("machine-1")},
	})

	_, ok := c.get("machine-1")
	assert.False(t, ok, "expected cache miss with zero TTL")
}

func TestMachineListCache_IsFreshBeforeUpdate(t *testing.T) {
	c := newMachineListCache(30 * time.Second)

	// Before any update, cache should not be fresh
	assert.False(t, c.isFresh(), "expected cache to not be fresh before any update")
}
