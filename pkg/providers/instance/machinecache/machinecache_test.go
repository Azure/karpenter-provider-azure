package machinecache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/samber/lo"
)

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
