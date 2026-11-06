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
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Azure/karpenter-provider-azure/pkg/utils/batcher"
)

func newTestClient(t *testing.T, api *fakeFleetsAPI) *Client {
	t.Helper()
	return newTestClientWithOptions(t, api, batcher.Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   2 * time.Second,
		MaxBatchSize: 10,
	})
}

func newTestClientWithOptions(t *testing.T, api *fakeFleetsAPI, opts batcher.Options) *Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewClient(ctx, api, "cluster", "node-rg", opts)
}

func TestClient_CoalescesCompatibleRequests(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{}
	const requestCount = 3
	client := newTestClientWithOptions(t, api, batcher.Options{
		IdleTimeout:  time.Hour,
		MaxTimeout:   time.Hour,
		MaxBatchSize: requestCount,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	responses := make([]FleetBatchResponse, requestCount)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := baseRequest()
			req.NodeClaimName = fmt.Sprintf("claim-%d", i)
			responses[i] = client.CreateVM(ctx, *req)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	g.Eventually(done).WithTimeout(15 * time.Second).Should(BeClosed())

	puts := api.recordedPuts()
	g.Expect(puts).To(HaveLen(1))
	g.Expect(targetCapacity(puts[0].body)).To(Equal(int32(requestCount)))
	for i, resp := range responses {
		g.Expect(resp.Error).NotTo(HaveOccurred())
		g.Expect(resp.SharedState.GetAssignment(fmt.Sprintf("claim-%d", i))).NotTo(BeNil())
	}
}

func TestClient_SeparatesIncompatibleRequests(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{}
	client := newTestClient(t, api)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, capacityType := range []string{"on-demand", "spot"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := baseRequest()
			req.NodeClaimName = fmt.Sprintf("claim-%d", i)
			req.CapacityType = capacityType
			errs[i] = client.CreateVM(context.Background(), *req).Error
		}()
	}
	wg.Wait()

	g.Expect(errs).To(HaveEach(Not(HaveOccurred())))
	g.Expect(api.recordedPuts()).To(HaveLen(2))
}

func TestClient_UsesPreparedBodiesForGroupingAndCreation(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{}
	client := newTestClient(t, api)

	requests := make([]*FleetVMProvisionRequest, 2)
	for i := range requests {
		req := baseRequest()
		req.NodeClaimName = fmt.Sprintf("claim-%d", i)
		body, err := BuildFleetBody(req, 1, req.Tags)
		g.Expect(err).NotTo(HaveOccurred())
		body.Properties.VMNamePrefix = lo.ToPtr(fmt.Sprintf("prepared-%d", i))
		req.FleetBody = body
		req.LaunchTemplate = nil
		requests[i] = req
	}

	responses := make([]FleetBatchResponse, len(requests))
	var wg sync.WaitGroup
	for i, req := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[i] = client.CreateVM(context.Background(), *req)
		}()
	}
	wg.Wait()

	puts := api.recordedPuts()
	g.Expect(puts).To(HaveLen(2))
	g.Expect(lo.Map(puts, func(put fakeFleetPut, _ int) string {
		return lo.FromPtr(put.body.Properties.VMNamePrefix)
	})).To(ConsistOf("prepared-0", "prepared-1"))
	for i, resp := range responses {
		g.Expect(resp.Error).NotTo(HaveOccurred())
		g.Expect(resp.SharedState.GetAssignment(fmt.Sprintf("claim-%d", i))).NotTo(BeNil())
	}
}

func TestClient_InvalidRequestFailsBeforeBatching(t *testing.T) {
	g := NewWithT(t)
	api := &fakeFleetsAPI{}
	client := newTestClient(t, api)
	req := baseRequest()
	req.LaunchTemplate = nil

	resp := client.CreateVM(context.Background(), *req)

	g.Expect(resp.Error).To(MatchError(ContainSubstring("enqueueing fleet request")))
	g.Expect(resp.SharedState).To(BeNil())
	g.Expect(api.recordedPuts()).To(BeEmpty())
}

func TestClient_CallerContextCanceled(t *testing.T) {
	g := NewWithT(t)
	client := newTestClientWithOptions(t, &fakeFleetsAPI{}, batcher.Options{
		IdleTimeout:  time.Hour,
		MaxTimeout:   time.Hour,
		MaxBatchSize: 2,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	responses := make(chan FleetBatchResponse, 1)
	go func() {
		responses <- client.CreateVM(ctx, *baseRequest())
	}()
	var resp FleetBatchResponse
	g.Eventually(responses).WithTimeout(5 * time.Second).Should(Receive(&resp))

	g.Expect(resp.Error).To(MatchError(context.Canceled))
}
