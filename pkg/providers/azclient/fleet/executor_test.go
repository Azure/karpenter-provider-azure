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

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet/v2"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

type fakeFleetPut struct {
	resourceGroup string
	name          string
	body          armcomputefleet.Fleet
}

// fakeFleetsAPI records Fleet PUTs and, by default, lists one successful VM per requested capacity.
type fakeFleetsAPI struct {
	mu        sync.Mutex
	puts      []fakeFleetPut
	listCalls []string

	putErr  func(body armcomputefleet.Fleet) error
	listErr error
	// pages overrides the listed VM pages for a Fleet.
	pages func(name string, body armcomputefleet.Fleet) [][]*armcomputefleet.VirtualMachine
}

func (f *fakeFleetsAPI) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, fleetName string, body armcomputefleet.Fleet, _ *armcomputefleet.FleetsClientBeginCreateOrUpdateOptions) (*runtime.Poller[armcomputefleet.FleetsClientCreateOrUpdateResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, fakeFleetPut{resourceGroup: resourceGroupName, name: fleetName, body: body})
	if f.putErr != nil {
		if err := f.putErr(body); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (f *fakeFleetsAPI) NewListVirtualMachinesPager(_ string, name string, _ *armcomputefleet.FleetsClientListVirtualMachinesOptions) *runtime.Pager[armcomputefleet.FleetsClientListVirtualMachinesResponse] {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, name)
	body := f.putFor(name).body
	listErr := f.listErr
	f.mu.Unlock()

	pages := [][]*armcomputefleet.VirtualMachine{defaultFleetVMs(name, body)}
	if f.pages != nil {
		pages = f.pages(name, body)
	}
	return runtime.NewPager(runtime.PagingHandler[armcomputefleet.FleetsClientListVirtualMachinesResponse]{
		More: func(page armcomputefleet.FleetsClientListVirtualMachinesResponse) bool {
			return page.NextLink != nil
		},
		Fetcher: func(_ context.Context, current *armcomputefleet.FleetsClientListVirtualMachinesResponse) (armcomputefleet.FleetsClientListVirtualMachinesResponse, error) {
			if listErr != nil {
				return armcomputefleet.FleetsClientListVirtualMachinesResponse{}, listErr
			}
			idx := 0
			if current != nil {
				idx, _ = strconv.Atoi(*current.NextLink)
			}
			resp := armcomputefleet.FleetsClientListVirtualMachinesResponse{}
			if idx < len(pages) {
				resp.Value = pages[idx]
			}
			if idx+1 < len(pages) {
				resp.NextLink = lo.ToPtr(strconv.Itoa(idx + 1))
			}
			return resp, nil
		},
	})
}

func (f *fakeFleetsAPI) putFor(name string) fakeFleetPut {
	for _, put := range f.puts {
		if put.name == name {
			return put
		}
	}
	return fakeFleetPut{}
}

func (f *fakeFleetsAPI) recordedPuts() []fakeFleetPut {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeFleetPut(nil), f.puts...)
}

func (f *fakeFleetsAPI) recordedListCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.listCalls...)
}

func targetCapacity(body armcomputefleet.Fleet) int32 {
	if body.Properties == nil {
		return 0
	}
	if body.Properties.RegularPriorityProfile != nil {
		return lo.FromPtr(body.Properties.RegularPriorityProfile.Capacity)
	}
	if body.Properties.SpotPriorityProfile != nil {
		return lo.FromPtr(body.Properties.SpotPriorityProfile.Capacity)
	}
	return 0
}

func defaultFleetVMs(name string, body armcomputefleet.Fleet) []*armcomputefleet.VirtualMachine {
	vms := make([]*armcomputefleet.VirtualMachine, 0, targetCapacity(body))
	for i := range int(targetCapacity(body)) {
		vms = append(vms, fleetVM(fmt.Sprintf("%s-vm-%d", name, i), "Standard_D4s_v3", "1"))
	}
	return vms
}

func fleetVM(name, size, zone string) *armcomputefleet.VirtualMachine {
	vm := &armcomputefleet.VirtualMachine{
		ID:              lo.ToPtr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/" + name),
		Name:            lo.ToPtr(name),
		Type:            lo.ToPtr("Microsoft.Compute/virtualMachines"),
		VMSize:          lo.ToPtr(size),
		OperationStatus: lo.ToPtr(armcomputefleet.VMOperationStatusSucceeded),
	}
	if zone != "" {
		vm.Zone = lo.ToPtr(zone)
	}
	return vm
}

func newTestBatch(t *testing.T, count int) *fleetBatch {
	t.Helper()
	template := baseRequest()
	body, err := BuildFleetBody(template, 1, template.Tags)
	if err != nil {
		t.Fatalf("building fleet test body: %v", err)
	}
	batch := &fleetBatch{ID: "batch-id", Key: "default/0123456789abcdef"}
	for i := range count {
		req := baseRequest()
		req.NodeClaimName = fmt.Sprintf("claim-%d", i)
		req.FleetBody = body
		batch.Requests = append(batch.Requests, &fleetBatchedRequest{
			Key:          batch.Key,
			Payload:      *req,
			ResponseChan: make(chan *batcher.Response[FleetBatchResponse], 1),
		})
	}
	return batch
}

func receiveResponse(g *WithT, req *fleetBatchedRequest) FleetBatchResponse {
	var resp *batcher.Response[FleetBatchResponse]
	g.Eventually(req.ResponseChan).WithTimeout(5 * time.Second).Should(Receive(&resp))
	g.Expect(resp.Err).NotTo(HaveOccurred())
	return resp.Payload
}

func newTestExecutor(api *fakeFleetsAPI) *executor {
	return newExecutor(api, "cluster", "node-rg")
}

func TestExecuteBatch_AssignsListedVMs(t *testing.T) {
	g := NewWithT(t)
	failed := fleetVM("vm-failed", "Standard_D8s_v3", "2")
	failed.OperationStatus = lo.ToPtr(armcomputefleet.VMOperationStatusFailed)
	failed.Error = &armcomputefleet.APIError{Code: lo.ToPtr("AllocationFailed")}
	api := &fakeFleetsAPI{pages: func(string, armcomputefleet.Fleet) [][]*armcomputefleet.VirtualMachine {
		return [][]*armcomputefleet.VirtualMachine{{
			failed,
			fleetVM("vm-a", "Standard_D4s_v3", "1"),
			fleetVM("vm-b", "Standard_D8s_v3", ""),
		}}
	}}
	batch := newTestBatch(t, 2)

	newTestExecutor(api).executeBatch(context.Background(), batch)

	puts := api.recordedPuts()
	g.Expect(puts).To(HaveLen(1))
	g.Expect(puts[0].resourceGroup).To(Equal("node-rg"))
	g.Expect(puts[0].name).To(MatchRegexp(`^fleet-cluster-01234567-[0-9a-f]{16}$`))
	g.Expect(targetCapacity(puts[0].body)).To(Equal(int32(2)))
	g.Expect(puts[0].body.Tags).To(HaveKeyWithValue(FleetNameTagKey, lo.ToPtr(puts[0].name)))
	g.Expect(puts[0].body.Tags).To(HaveKeyWithValue("env", lo.ToPtr("test")))
	g.Expect(batch.Requests[0].Payload.Tags).NotTo(HaveKey(FleetNameTagKey))
	g.Expect(api.recordedListCalls()).To(Equal([]string{puts[0].name}))

	first := receiveResponse(g, batch.Requests[0])
	second := receiveResponse(g, batch.Requests[1])
	g.Expect(first.Error).NotTo(HaveOccurred())
	g.Expect(first.SharedState).To(BeIdenticalTo(second.SharedState))
	state := first.SharedState
	g.Expect(state.GetError()).NotTo(HaveOccurred())
	g.Expect(state.FleetName()).To(Equal(puts[0].name))

	claim0 := state.GetAssignment("claim-0")
	g.Expect(claim0.SKU).To(Equal("Standard_D4s_v3"))
	g.Expect(claim0.Zone).To(Equal("eastus-1"))
	g.Expect(*claim0.VM.Name).To(Equal("vm-a"))
	g.Expect(*claim0.VM.ID).To(HaveSuffix("/virtualMachines/vm-a"))
	g.Expect(*claim0.VM.Location).To(Equal("eastus"))
	claim1 := state.GetAssignment("claim-1")
	g.Expect(claim1.SKU).To(Equal("Standard_D8s_v3"))
	g.Expect(*claim1.VM.Name).To(Equal("vm-b"))
	g.Expect(state.Surplus()).To(BeEmpty())
}

func TestExecuteBatch_ListsAllPages(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{pages: func(string, armcomputefleet.Fleet) [][]*armcomputefleet.VirtualMachine {
		return [][]*armcomputefleet.VirtualMachine{
			{fleetVM("vm-a", "Standard_D4s_v3", "1")},
			{nil, {VMSize: lo.ToPtr("Standard_D4s_v3")}},
			{fleetVM("vm-b", "Standard_D4s_v3", "2")},
		}
	}}
	batch := newTestBatch(t, 2)

	newTestExecutor(api).executeBatch(context.Background(), batch)

	state := receiveResponse(g, batch.Requests[0]).SharedState
	g.Expect(*state.GetAssignment("claim-0").VM.Name).To(Equal("vm-a"))
	g.Expect(*state.GetAssignment("claim-1").VM.Name).To(Equal("vm-b"))
}

func TestExecuteBatch_FewerVMsThanRequests(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{pages: func(string, armcomputefleet.Fleet) [][]*armcomputefleet.VirtualMachine {
		return [][]*armcomputefleet.VirtualMachine{{fleetVM("vm-a", "Standard_D4s_v3", "1")}}
	}}
	batch := newTestBatch(t, 2)

	newTestExecutor(api).executeBatch(context.Background(), batch)

	state := receiveResponse(g, batch.Requests[1]).SharedState
	g.Expect(state.GetError()).NotTo(HaveOccurred())
	g.Expect(state.GetAssignment("claim-0")).NotTo(BeNil())
	g.Expect(state.GetAssignment("claim-1")).To(BeNil())
}

func TestExecuteBatch_NoVMs(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{pages: func(string, armcomputefleet.Fleet) [][]*armcomputefleet.VirtualMachine {
		return [][]*armcomputefleet.VirtualMachine{{}}
	}}
	batch := newTestBatch(t, 2)

	newTestExecutor(api).executeBatch(context.Background(), batch)

	for _, req := range batch.Requests {
		resp := receiveResponse(g, req)
		g.Expect(resp.Error).NotTo(HaveOccurred())
		g.Expect(resp.SharedState.GetError()).To(MatchError(ContainSubstring("created no VMs")))
	}
}

func TestExecuteBatch_DistributesErrors(t *testing.T) {
	for _, tt := range []struct {
		name          string
		api           *fakeFleetsAPI
		mutate        func(*fleetBatch)
		expectedErr   string
		expectedPuts  int
		expectedLists int
	}{
		{
			name:         "missing body",
			api:          &fakeFleetsAPI{},
			mutate:       func(b *fleetBatch) { b.Requests[0].Payload.FleetBody = nil },
			expectedErr:  "preparing fleet body: fleet body and properties are required",
			expectedPuts: 0,
		},
		{
			name: "missing properties",
			api:  &fakeFleetsAPI{},
			mutate: func(b *fleetBatch) {
				b.Requests[0].Payload.FleetBody = &armcomputefleet.Fleet{Location: lo.ToPtr("eastus")}
			},
			expectedErr:  "preparing fleet body: fleet body and properties are required",
			expectedPuts: 0,
		},
		{
			name: "missing location",
			api:  &fakeFleetsAPI{},
			mutate: func(b *fleetBatch) {
				body := *b.Requests[0].Payload.FleetBody
				body.Location = nil
				b.Requests[0].Payload.FleetBody = &body
			},
			expectedErr:  "preparing fleet body: fleet location is required",
			expectedPuts: 0,
		},
		{
			name: "missing priority profile",
			api:  &fakeFleetsAPI{},
			mutate: func(b *fleetBatch) {
				b.Requests[0].Payload.FleetBody = &armcomputefleet.Fleet{
					Location:   lo.ToPtr("eastus"),
					Properties: &armcomputefleet.FleetProperties{},
				}
			},
			expectedErr:  "preparing fleet body: exactly one fleet priority profile is required",
			expectedPuts: 0,
		},
		{
			name: "multiple priority profiles",
			api:  &fakeFleetsAPI{},
			mutate: func(b *fleetBatch) {
				b.Requests[0].Payload.FleetBody = &armcomputefleet.Fleet{
					Location: lo.ToPtr("eastus"),
					Properties: &armcomputefleet.FleetProperties{
						RegularPriorityProfile: &armcomputefleet.RegularPriorityProfile{},
						SpotPriorityProfile:    &armcomputefleet.SpotPriorityProfile{},
					},
				}
			},
			expectedErr:  "preparing fleet body: exactly one fleet priority profile is required",
			expectedPuts: 0,
		},
		{
			name: "create failure",
			api: &fakeFleetsAPI{putErr: func(armcomputefleet.Fleet) error {
				return errors.New("quota exceeded")
			}},
			expectedErr:  "quota exceeded",
			expectedPuts: 1,
		},
		{
			name:          "list failure",
			api:           &fakeFleetsAPI{listErr: errors.New("list throttled")},
			expectedErr:   "list throttled",
			expectedPuts:  1,
			expectedLists: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			batch := newTestBatch(t, 2)
			if tt.mutate != nil {
				tt.mutate(batch)
			}

			newTestExecutor(tt.api).executeBatch(context.Background(), batch)

			g.Expect(tt.api.recordedPuts()).To(HaveLen(tt.expectedPuts))
			g.Expect(tt.api.recordedListCalls()).To(HaveLen(tt.expectedLists))
			for _, req := range batch.Requests {
				resp := receiveResponse(g, req)
				g.Expect(resp.SharedState).To(BeNil())
				g.Expect(resp.Error).To(MatchError(ContainSubstring(tt.expectedErr)))
			}
		})
	}
}

func TestExecuteBatch_EmptyBatch(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{}

	newTestExecutor(api).executeBatch(context.Background(), &fleetBatch{})
	newTestExecutor(api).executeBatch(context.Background(), nil)

	g.Expect(api.recordedPuts()).To(BeEmpty())
}

func TestCreateFleet_ReusesPreparedBody(t *testing.T) {
	for _, tt := range []struct {
		name         string
		capacityType string
		nilTags      bool
	}{
		{name: "on-demand with existing fleet tag", capacityType: "on-demand"},
		{name: "spot with nil tags", capacityType: "spot", nilTags: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			req := baseRequest()
			req.CapacityType = tt.capacityType
			if tt.nilTags {
				req.Tags = nil
			} else {
				req.Tags[FleetNameTagKey] = lo.ToPtr("old-fleet")
			}
			body, err := BuildFleetBody(req, 1, req.Tags)
			g.Expect(err).NotTo(HaveOccurred())
			body.Properties.VMNamePrefix = lo.ToPtr("prepared-prefix")
			original, err := json.Marshal(body)
			g.Expect(err).NotTo(HaveOccurred())

			api := &fakeFleetsAPI{}
			batch := newTestBatch(t, 2)
			for _, request := range batch.Requests {
				request.Payload.FleetBody = body
				request.Payload.LaunchTemplate = nil
				request.Payload.Location = "westus"
			}

			newTestExecutor(api).executeBatch(context.Background(), batch)

			puts := api.recordedPuts()
			g.Expect(puts).To(HaveLen(1))
			put := puts[0]
			g.Expect(targetCapacity(put.body)).To(Equal(int32(2)))
			g.Expect(put.body.Properties.VMNamePrefix).To(Equal(lo.ToPtr("prepared-prefix")))
			g.Expect(put.body.Tags).To(HaveKeyWithValue(FleetNameTagKey, lo.ToPtr(put.name)))
			g.Expect(put.body.Properties).NotTo(BeIdenticalTo(body.Properties))
			if tt.capacityType == "spot" {
				g.Expect(put.body.Properties.SpotPriorityProfile).NotTo(BeIdenticalTo(body.Properties.SpotPriorityProfile))
			} else {
				g.Expect(put.body.Properties.RegularPriorityProfile).NotTo(BeIdenticalTo(body.Properties.RegularPriorityProfile))
				g.Expect(put.body.Tags).To(HaveKeyWithValue("env", lo.ToPtr("test")))
			}
			after, err := json.Marshal(body)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(after).To(Equal(original))
			for _, request := range batch.Requests {
				resp := receiveResponse(g, request)
				g.Expect(resp.Error).NotTo(HaveOccurred())
				assignment := resp.SharedState.GetAssignment(request.Payload.NodeClaimName)
				g.Expect(assignment).NotTo(BeNil())
				g.Expect(assignment.VM.Location).To(Equal(lo.ToPtr("eastus")))
			}
		})
	}
}

func TestCreateFleet_CapacityLimit(t *testing.T) {
	for _, tt := range []struct {
		name  string
		count int
	}{
		{name: "at limit", count: MaxFleetCapacity},
		{name: "over limit", count: MaxFleetCapacity + 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			api := &fakeFleetsAPI{}
			batch := newTestBatch(t, tt.count)

			newTestExecutor(api).createFleet(context.Background(), batch.Key, batch.Requests, logr.Discard())

			if tt.count > MaxFleetCapacity {
				g.Expect(api.recordedPuts()).To(BeEmpty())
				g.Expect(api.recordedListCalls()).To(BeEmpty())
				for _, req := range batch.Requests {
					resp := receiveResponse(g, req)
					g.Expect(resp.SharedState).To(BeNil())
					g.Expect(resp.Error).To(MatchError(fmt.Sprintf("fleet capacity must not exceed %d", MaxFleetCapacity)))
				}
				return
			}
			puts := api.recordedPuts()
			g.Expect(puts).To(HaveLen(1))
			g.Expect(targetCapacity(puts[0].body)).To(Equal(int32(MaxFleetCapacity)))
			for _, req := range batch.Requests {
				resp := receiveResponse(g, req)
				g.Expect(resp.Error).NotTo(HaveOccurred())
				g.Expect(resp.SharedState.GetAssignment(req.Payload.NodeClaimName)).NotTo(BeNil())
			}
		})
	}
}

func TestExecuteBatch_SplitsLargeBatches(t *testing.T) {
	for _, capacityType := range []string{"on-demand", "spot"} {
		t.Run(capacityType, func(t *testing.T) {
			g := NewWithT(t)
			api := &fakeFleetsAPI{}
			exec := newTestExecutor(api)
			exec.maxFleetCapacity = 2
			batch := newTestBatch(t, 5)
			request := baseRequest()
			request.CapacityType = capacityType
			body, err := BuildFleetBody(request, 1, request.Tags)
			g.Expect(err).NotTo(HaveOccurred())
			for _, req := range batch.Requests {
				req.Payload.CapacityType = capacityType
				req.Payload.FleetBody = body
			}

			exec.executeBatch(context.Background(), batch)

			puts := api.recordedPuts()
			g.Expect(lo.Map(puts, func(p fakeFleetPut, _ int) int32 { return targetCapacity(p.body) })).To(ConsistOf(int32(2), int32(2), int32(1)))
			g.Expect(lo.Uniq(lo.Map(puts, func(p fakeFleetPut, _ int) string { return p.name }))).To(HaveLen(3))
			for _, put := range puts {
				g.Expect(put.body.Tags).To(HaveKeyWithValue(FleetNameTagKey, lo.ToPtr(put.name)))
			}
			g.Expect(targetCapacity(*body)).To(Equal(int32(1)))
			g.Expect(body.Tags).NotTo(HaveKey(FleetNameTagKey))

			assignedVMs := map[string]string{}
			for i, req := range batch.Requests {
				resp := receiveResponse(g, req)
				g.Expect(resp.Error).NotTo(HaveOccurred())
				assignment := resp.SharedState.GetAssignment(fmt.Sprintf("claim-%d", i))
				g.Expect(assignment).NotTo(BeNil())
				g.Expect(*assignment.VM.Name).To(HavePrefix(resp.SharedState.FleetName()))
				assignedVMs[*assignment.VM.Name] = req.Payload.NodeClaimName
			}
			g.Expect(assignedVMs).To(HaveLen(5))
		})
	}
}

func TestExecuteBatch_SplitSubBatchFailureIsIsolated(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{putErr: func(body armcomputefleet.Fleet) error {
		if targetCapacity(body) == 1 {
			return errors.New("sub-batch failed")
		}
		return nil
	}}
	exec := newTestExecutor(api)
	exec.maxFleetCapacity = 2
	batch := newTestBatch(t, 3)

	exec.executeBatch(context.Background(), batch)

	for i, req := range batch.Requests[:2] {
		resp := receiveResponse(g, req)
		g.Expect(resp.Error).NotTo(HaveOccurred())
		g.Expect(resp.SharedState.GetAssignment(fmt.Sprintf("claim-%d", i))).NotTo(BeNil())
	}
	resp := receiveResponse(g, batch.Requests[2])
	g.Expect(resp.Error).To(MatchError(ContainSubstring("sub-batch failed")))
}

func TestExecuteBatch_SplitSubBatchPanicIsRecovered(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{putErr: func(body armcomputefleet.Fleet) error {
		if targetCapacity(body) == 1 {
			panic("boom")
		}
		return nil
	}}
	exec := newTestExecutor(api)
	exec.maxFleetCapacity = 2
	batch := newTestBatch(t, 3)

	exec.executeBatch(context.Background(), batch)

	g.Expect(receiveResponse(g, batch.Requests[0]).Error).NotTo(HaveOccurred())
	var resp *batcher.Response[FleetBatchResponse]
	g.Eventually(batch.Requests[2].ResponseChan).Should(Receive(&resp))
	g.Expect(resp.Err).To(MatchError(ContainSubstring("fleet sub-batch panicked: boom")))
}

func TestFleetName(t *testing.T) {
	maxClusterLength := 80 - len("fleet--01234567-") - 16
	maxShortHashClusterLength := 80 - len("fleet--abc-") - 16
	limitedClusterName := strings.Repeat("c", maxClusterLength)

	for _, tt := range []struct {
		name                string
		clusterName         string
		batchKey            string
		expectedClusterName string
		expectedHash        string
		expectMaxLength     bool
	}{
		{
			name:                "full batch hash",
			clusterName:         "cluster",
			batchKey:            "default/0123456789abcdef",
			expectedClusterName: "cluster",
			expectedHash:        "01234567",
		},
		{
			name:                "short batch hash",
			clusterName:         "cluster",
			batchKey:            "default/abc",
			expectedClusterName: "cluster",
			expectedHash:        "abc",
		},
		{
			name:                "cluster name at limit",
			clusterName:         limitedClusterName,
			batchKey:            "default/0123456789abcdef",
			expectedClusterName: limitedClusterName,
			expectedHash:        "01234567",
			expectMaxLength:     true,
		},
		{
			name:                "cluster name over limit",
			clusterName:         limitedClusterName + "extra",
			batchKey:            "default/0123456789abcdef",
			expectedClusterName: limitedClusterName,
			expectedHash:        "01234567",
			expectMaxLength:     true,
		},
		{
			name:                "long cluster name with short batch hash",
			clusterName:         strings.Repeat("c", 63),
			batchKey:            "default/abc",
			expectedClusterName: strings.Repeat("c", maxShortHashClusterLength),
			expectedHash:        "abc",
			expectMaxLength:     true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			name, err := fleetName(tt.clusterName, tt.batchKey)
			g.Expect(err).NotTo(HaveOccurred())

			prefix := fmt.Sprintf("fleet-%s-%s-", tt.expectedClusterName, tt.expectedHash)
			g.Expect(name).To(HavePrefix(prefix))
			g.Expect(strings.TrimPrefix(name, prefix)).To(MatchRegexp(`^[0-9a-f]{16}$`))
			g.Expect(len(name)).To(BeNumerically("<=", 80))
			if tt.expectMaxLength {
				g.Expect(len(name)).To(Equal(80))
			}
		})
	}

	g := NewWithT(t)
	names := make(map[string]struct{}, 100)
	for range 100 {
		name, err := fleetName("cluster", "default/0123456789abcdef")
		g.Expect(err).NotTo(HaveOccurred())
		names[name] = struct{}{}
	}
	g.Expect(names).To(HaveLen(100))
}
