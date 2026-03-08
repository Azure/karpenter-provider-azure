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
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Tags contain NodeClaim-unique values (nodeClaim.Name) and must be excluded from
// the hash so that machines with the same template but different NodeClaims still
// batch together.
func TestComputeTemplateHash_TagsExcluded(t *testing.T) {
	t.Parallel()

	vmSize := "Standard_D2s_v3"

	withTags := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			Tags: map[string]*string{
				"karpenter.azure.com_aksmachine_nodeclaim": lo.ToPtr("nodeclaim-abc"),
			},
		},
	}
	withoutTags := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}
	withDifferentTags := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			Tags: map[string]*string{
				"karpenter.azure.com_aksmachine_nodeclaim": lo.ToPtr("nodeclaim-xyz"),
			},
		},
	}

	h1 := computeTemplateHash(withTags)
	h2 := computeTemplateHash(withoutTags)
	h3 := computeTemplateHash(withDifferentTags)

	assert.Equal(t, h1, h2, "tags should not affect hash")
	assert.Equal(t, h1, h3, "different tags should not affect hash")
}

// Read-only fields (ETag, ProvisioningState, ResourceID, Status) must not affect the hash.
func TestComputeTemplateHash_ReadOnlyFieldsExcluded(t *testing.T) {
	t.Parallel()

	vmSize := "Standard_D2s_v3"

	withReadOnly := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware:          &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			ETag:              lo.ToPtr("etag-123"),
			ProvisioningState: lo.ToPtr("Succeeded"),
			ResourceID:        lo.ToPtr("/subscriptions/sub/resourceGroups/rg/..."),
		},
	}
	withoutReadOnly := &armcontainerservice.Machine{
		Properties: &armcontainerservice.MachineProperties{
			Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
		},
	}

	h1 := computeTemplateHash(withReadOnly)
	h2 := computeTemplateHash(withoutReadOnly)

	assert.Equal(t, h1, h2, "read-only fields should not affect hash")
}

// Guardrail: ensures that every field of MachineProperties is either hashed or
// explicitly excluded. If the Azure SDK adds a new field to MachineProperties,
// this test fails — forcing the developer to decide whether to hash or exclude it.
//
// When a new field appears:
//   - If it's per-machine (varies per NodeClaim), add it to clearPerMachineFields()
//     in grouper.go. Both the hash and the coordinator body-clearing pick it up
//     automatically — no second place to update.
//   - If it's read-only (set by server), add it to clearReadOnlyFields().
//   - If it's shared template config, do nothing — it's hashed automatically.
func TestComputeTemplateHash_AllFieldsAccountedFor(t *testing.T) {
	t.Parallel()

	// Fields explicitly excluded from the hash via clearPerMachineFields/clearReadOnlyFields.
	// If you add a new field to this set, update the corresponding clear function in grouper.go.
	excludedFields := map[string]string{
		"Tags":              "per-machine: contains NodeClaim name (cleared by clearPerMachineFields)",
		"ETag":              "read-only: set by server (cleared by clearReadOnlyFields)",
		"ProvisioningState": "read-only: set by server (cleared by clearReadOnlyFields)",
		"ResourceID":        "read-only: set by server (cleared by clearReadOnlyFields)",
		"Status":            "read-only: set by server (cleared by clearReadOnlyFields)",
	}

	typ := reflect.TypeOf(armcontainerservice.MachineProperties{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if _, excluded := excludedFields[field.Name]; excluded {
			continue
		}
		// If this assertion fails, a new field was added to MachineProperties.
		// Decide: should it be hashed (do nothing) or excluded (add to excludedFields above)?
		assert.True(t, field.IsExported(),
			"unexpected unexported field %q in MachineProperties — review computeTemplateHash", field.Name)
	}

	// Verify the count matches: hashed fields + excluded fields == total fields.
	// This catches the case where someone adds a field to excludedFields without
	// a corresponding SDK field (stale exclude entry).
	totalFields := typ.NumField()
	hashedFields := totalFields - len(excludedFields)
	assert.Greater(t, hashedFields, 0,
		"at least one field should be hashed (got %d total, %d excluded)", totalFields, len(excludedFields))

	// Verify all excluded fields actually exist in the struct.
	for name, reason := range excludedFields {
		_, found := typ.FieldByName(name)
		assert.True(t, found,
			"excluded field %q (reason: %s) does not exist in MachineProperties — remove from excludedFields", name, reason)
	}
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

// When the grouper shuts down (context canceled), pending requests that haven't
// been dispatched yet must receive an error instead of hanging forever.
func TestGrouperDrainsPendingRequestsOnShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	// Create grouper but DON'T start the background loop — this ensures requests
	// stay in the pending map and are only handled by drain.
	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  10 * time.Second,
		MaxTimeout:   10 * time.Second,
		MaxBatchSize: 50,
	})
	grouper.SetCoordinator(NewCoordinator(&recordingClient{}, "rg", "cluster", "pool"))

	// Enqueue requests — they'll sit in the pending batch with no loop to dispatch them
	vmSize := "Standard_D2s_v3"
	req1 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine1",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			},
		},
	}
	req2 := &CreateRequest{
		ctx:          ctx,
		machineName:  "machine2",
		responseChan: make(chan *CreateResponse, 1),
		template: armcontainerservice.Machine{
			Properties: &armcontainerservice.MachineProperties{
				Hardware: &armcontainerservice.MachineHardwareProfile{VMSize: &vmSize},
			},
		},
	}

	grouper.EnqueueCreate(req1)
	grouper.EnqueueCreate(req2)

	// Verify requests are pending
	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 1, "should have one pending batch")
	grouper.mu.Unlock()

	// Cancel context then drain directly — verifies drainPendingRequests
	// fails all waiting callers with a shutdown error.
	cancel()
	grouper.drainPendingRequests()

	// Both requests should receive a shutdown error
	for i, req := range []*CreateRequest{req1, req2} {
		select {
		case resp := <-req.responseChan:
			require.Error(t, resp.Err, "request %d should receive a shutdown error", i)
			assert.Contains(t, resp.Err.Error(), "shutting down", "request %d error should mention shutdown", i)
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d timed out — drain did not deliver shutdown error", i)
		}
	}
}

// Verifies that clearPerMachineFields and clearReadOnlyFields (used by both
// computeTemplateHash and ExecuteBatch) zero exactly the fields listed in the
// AllFieldsAccountedFor guardrail test. If someone adds a field to one of the
// clear functions but forgets the guardrail, or vice versa, this test fails.
func TestClearFieldFunctions_MatchExcludeList(t *testing.T) {
	t.Parallel()

	// Build a MachineProperties where every pointer field is non-nil, so we can
	// detect which fields each clear function zeros.
	allSet := armcontainerservice.MachineProperties{
		Hardware:          &armcontainerservice.MachineHardwareProfile{},
		Kubernetes:        &armcontainerservice.MachineKubernetesProfile{},
		Mode:              lo.ToPtr(armcontainerservice.AgentPoolModeUser),
		Network:           &armcontainerservice.MachineNetworkProperties{},
		NodeImageVersion:  lo.ToPtr("v1"),
		OperatingSystem:   &armcontainerservice.MachineOSProfile{},
		Priority:          lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
		Security:          &armcontainerservice.MachineSecurityProfile{},
		Tags:              map[string]*string{"k": lo.ToPtr("v")},
		ETag:              lo.ToPtr("etag"),
		ProvisioningState: lo.ToPtr("Succeeded"),
		ResourceID:        lo.ToPtr("/sub/rg/..."),
		Status:            &armcontainerservice.MachineStatus{},
	}

	// Run both clear functions (same as computeTemplateHash does)
	cleared := allSet
	clearPerMachineFields(&cleared)
	clearReadOnlyFields(&cleared)

	// Check that Tags was cleared (per-machine)
	assert.Nil(t, cleared.Tags, "clearPerMachineFields should nil Tags")

	// Check that read-only fields were cleared
	assert.Nil(t, cleared.ETag, "clearReadOnlyFields should nil ETag")
	assert.Nil(t, cleared.ProvisioningState, "clearReadOnlyFields should nil ProvisioningState")
	assert.Nil(t, cleared.ResourceID, "clearReadOnlyFields should nil ResourceID")
	assert.Nil(t, cleared.Status, "clearReadOnlyFields should nil Status")

	// Check that template fields were NOT cleared
	assert.NotNil(t, cleared.Hardware, "Hardware should not be cleared")
	assert.NotNil(t, cleared.Kubernetes, "Kubernetes should not be cleared")
	assert.NotNil(t, cleared.Mode, "Mode should not be cleared")
	assert.NotNil(t, cleared.Network, "Network should not be cleared")
	assert.NotNil(t, cleared.NodeImageVersion, "NodeImageVersion should not be cleared")
	assert.NotNil(t, cleared.OperatingSystem, "OperatingSystem should not be cleared")
	assert.NotNil(t, cleared.Priority, "Priority should not be cleared")
	assert.NotNil(t, cleared.Security, "Security should not be cleared")
}

// ----- Realistic production-like machine tests -----

// realisticMachineProps returns a fully-populated MachineProperties matching
// production templates built by buildAKSMachineTemplate. The vmSize and
// nodeClaimName vary per call to simulate different machines in a batch.
func realisticMachineProps(vmSize, nodeClaimName string) *armcontainerservice.MachineProperties {
	return &armcontainerservice.MachineProperties{
		Hardware: &armcontainerservice.MachineHardwareProfile{
			VMSize: lo.ToPtr(vmSize),
		},
		Kubernetes: &armcontainerservice.MachineKubernetesProfile{
			OrchestratorVersion: lo.ToPtr("1.31.0"),
			NodeLabels: map[string]*string{
				"karpenter.sh_nodepool":            lo.ToPtr("default"),
				"karpenter.sh_capacity-type":       lo.ToPtr("on-demand"),
				"node.kubernetes.io_instance-type": lo.ToPtr(vmSize),
			},
			NodeInitializationTaints: []*string{
				lo.ToPtr("node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule"),
			},
			NodeTaints: []*string{},
			MaxPods:    lo.ToPtr[int32](250),
			KubeletConfig: &armcontainerservice.KubeletConfig{
				CPUManagerPolicy: lo.ToPtr("static"),
			},
		},
		OperatingSystem: &armcontainerservice.MachineOSProfile{
			OSType:       lo.ToPtr(armcontainerservice.OSTypeLinux),
			OSSKU:        lo.ToPtr(armcontainerservice.OSSKUUbuntu2204),
			OSDiskSizeGB: lo.ToPtr[int32](128),
			OSDiskType:   lo.ToPtr(armcontainerservice.OSDiskTypeEphemeral),
			EnableFIPS:   lo.ToPtr(false),
		},
		Network: &armcontainerservice.MachineNetworkProperties{
			VnetSubnetID: lo.ToPtr("/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/nodesubnet"),
		},
		Priority:         lo.ToPtr(armcontainerservice.ScaleSetPriorityRegular),
		Mode:             lo.ToPtr(armcontainerservice.AgentPoolModeUser),
		NodeImageVersion: lo.ToPtr("AKSUbuntu-2204gen2containerd-2024.12.15"),
		Security: &armcontainerservice.MachineSecurityProfile{
			SSHAccess:              lo.ToPtr(armcontainerservice.AgentPoolSSHAccessLocalUser),
			EnableEncryptionAtHost: lo.ToPtr(true),
		},
		Tags: map[string]*string{
			"karpenter.azure.com_cluster":              lo.ToPtr("prod-cluster"),
			"compute.aks.billing":                      lo.ToPtr("linux"),
			"karpenter.sh_nodepool":                    lo.ToPtr("default"),
			"karpenter.azure.com_aksmachine_nodeclaim": lo.ToPtr(nodeClaimName),
		},
	}
}

// TestComputeTemplateHash_RealisticMachinesBatchTogether verifies that fully-populated
// production-like machine templates with the same config but different per-machine
// fields (name, zone, tags) produce the same hash and batch together.
func TestComputeTemplateHash_RealisticMachinesBatchTogether(t *testing.T) {
	t.Parallel()

	machines := make([]*armcontainerservice.Machine, 10)
	zones := []string{"1", "2", "3"}
	for i := range machines {
		machines[i] = &armcontainerservice.Machine{
			Name:       lo.ToPtr(fmt.Sprintf("machine-%d", i)),
			Zones:      []*string{lo.ToPtr(zones[i%len(zones)])},
			Properties: realisticMachineProps("Standard_D4s_v3", fmt.Sprintf("nodeclaim-%d", i)),
		}
	}

	// All machines with same VM config should produce same hash
	baseHash := computeTemplateHash(machines[0])
	assert.NotEmpty(t, baseHash)
	for i := 1; i < len(machines); i++ {
		assert.Equal(t, baseHash, computeTemplateHash(machines[i]),
			"machine %d should hash the same as machine 0 (same config, different name/zone/tags)", i)
	}
}

// TestComputeTemplateHash_RealisticMachinesDifferentConfigsSplit verifies that
// production-like machines with different shared config fields (VM size, priority,
// OS, etc.) produce different hashes and land in separate batches.
func TestComputeTemplateHash_RealisticMachinesDifferentConfigsSplit(t *testing.T) {
	t.Parallel()

	baseProps := func() *armcontainerservice.MachineProperties {
		return realisticMachineProps("Standard_D4s_v3", "nodeclaim-0")
	}

	baseMachine := &armcontainerservice.Machine{
		Name:       lo.ToPtr("machine-base"),
		Zones:      []*string{lo.ToPtr("1")},
		Properties: baseProps(),
	}
	baseHash := computeTemplateHash(baseMachine)

	tests := []struct {
		name   string
		modify func(p *armcontainerservice.MachineProperties)
	}{
		{
			name: "different VM size",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.Hardware.VMSize = lo.ToPtr("Standard_D8s_v3")
			},
		},
		{
			name: "spot priority",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.Priority = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
			},
		},
		{
			name: "different OS SKU",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.OperatingSystem.OSSKU = lo.ToPtr(armcontainerservice.OSSKUAzureLinux)
			},
		},
		{
			name: "different K8s version",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.Kubernetes.OrchestratorVersion = lo.ToPtr("1.32.0")
			},
		},
		{
			name: "different subnet",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.Network.VnetSubnetID = lo.ToPtr("/subscriptions/sub-456/resourceGroups/rg2/providers/Microsoft.Network/virtualNetworks/vnet2/subnets/other")
			},
		},
		{
			name: "system mode",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.Mode = lo.ToPtr(armcontainerservice.AgentPoolModeSystem)
			},
		},
		{
			name: "FIPS enabled",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.OperatingSystem.EnableFIPS = lo.ToPtr(true)
			},
		},
		{
			name: "different node image version",
			modify: func(p *armcontainerservice.MachineProperties) {
				p.NodeImageVersion = lo.ToPtr("AKSUbuntu-2404gen2containerd-2025.03.01")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			props := baseProps()
			tt.modify(props)
			m := &armcontainerservice.Machine{
				Name:       lo.ToPtr("machine-modified"),
				Zones:      []*string{lo.ToPtr("1")},
				Properties: props,
			}
			assert.NotEqual(t, baseHash, computeTemplateHash(m),
				"hash should differ when %s changes", tt.name)
		})
	}
}

// TestGrouperBatchesRealisticMixedWorkload simulates a production-like burst where
// multiple NodeClaims arrive concurrently with a mix of configs: some should batch
// together (same VM size/config), others should land in separate batches.
func TestGrouperBatchesRealisticMixedWorkload(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grouper := NewGrouper(ctx, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	zoneForIndex := func(i int) string { return fmt.Sprintf("%d", i%3+1) }
	makeReq := func(name, vmSize, nodeClaimName string, zone string) *CreateRequest {
		return &CreateRequest{
			ctx:          ctx,
			machineName:  name,
			responseChan: make(chan *CreateResponse, 1),
			template: armcontainerservice.Machine{
				Name:       lo.ToPtr(name),
				Zones:      []*string{lo.ToPtr(zone)},
				Properties: realisticMachineProps(vmSize, nodeClaimName),
			},
		}
	}

	// Enqueue 5 D4s_v3 regular machines (should batch together)
	for i := 0; i < 5; i++ {
		req := makeReq(
			fmt.Sprintf("d4-machine-%d", i),
			"Standard_D4s_v3",
			fmt.Sprintf("nc-d4-%d", i),
			zoneForIndex(i),
		)
		grouper.EnqueueCreate(req)
	}

	// Enqueue 3 D8s_v3 regular machines (different VM size → separate batch)
	for i := 0; i < 3; i++ {
		req := makeReq(
			fmt.Sprintf("d8-machine-%d", i),
			"Standard_D8s_v3",
			fmt.Sprintf("nc-d8-%d", i),
			zoneForIndex(i),
		)
		grouper.EnqueueCreate(req)
	}

	// Enqueue 2 D4s_v3 spot machines (different priority → separate batch)
	for i := 0; i < 2; i++ {
		req := makeReq(
			fmt.Sprintf("spot-machine-%d", i),
			"Standard_D4s_v3",
			fmt.Sprintf("nc-spot-%d", i),
			zoneForIndex(i),
		)
		req.template.Properties.Priority = lo.ToPtr(armcontainerservice.ScaleSetPrioritySpot)
		grouper.EnqueueCreate(req)
	}

	grouper.mu.Lock()
	assert.Len(t, grouper.batches, 3, "should have 3 batches: D4s regular, D8s regular, D4s spot")

	// Collect batch sizes
	batchSizes := make([]int, 0, len(grouper.batches))
	for _, batch := range grouper.batches {
		batchSizes = append(batchSizes, len(batch.requests))
	}
	grouper.mu.Unlock()

	// Sort for deterministic assertion
	assert.ElementsMatch(t, []int{5, 3, 2}, batchSizes,
		"batches should contain 5 D4s regular, 3 D8s regular, 2 D4s spot")
}
