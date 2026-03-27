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

package batcher

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testItem is a simple item type for testing.
type testItem struct {
	Group string // used as the grouping key
	Name  string // per-item identifier
}

func testKeyFunc(item *testItem) string {
	return item.Group
}

func makeTestReq(ctx context.Context, group, name string) *BatchedRequest[testItem, struct{}] {
	return &BatchedRequest[testItem, struct{}]{
		ctx:          ctx,
		Payload:      testItem{Group: group, Name: name},
		ResponseChan: make(chan *Response[struct{}], 1),
	}
}

func TestBatcherEnqueue(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var executed []*Batch[testItem, struct{}]

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		mu.Lock()
		executed = append(executed, batch)
		mu.Unlock()
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	req := makeTestReq(ctx, "group-a", "item-1")
	ch := b.Enqueue(req)
	assert.NotNil(t, ch)

	// Verify the request is in the internal map
	b.mu.Lock()
	assert.Len(t, b.pendingBatches, 1, "should have one pending batch")
	b.mu.Unlock()
}

func TestBatcherGroupsSameKey(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	b.Enqueue(makeTestReq(ctx, "group-a", "item-1"))
	b.Enqueue(makeTestReq(ctx, "group-a", "item-2"))

	b.mu.Lock()
	assert.Len(t, b.pendingBatches, 1, "same key → one batch")
	for _, batch := range b.pendingBatches {
		assert.Len(t, batch.Requests, 2, "batch should contain both requests")
	}
	b.mu.Unlock()
}

func TestBatcherSeparatesDifferentKeys(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  100 * time.Millisecond,
		MaxTimeout:   1 * time.Second,
		MaxBatchSize: 50,
	})

	b.Enqueue(makeTestReq(ctx, "group-a", "item-1"))
	b.Enqueue(makeTestReq(ctx, "group-b", "item-2"))

	b.mu.Lock()
	assert.Len(t, b.pendingBatches, 2, "different keys → two batches")
	b.mu.Unlock()
}

func TestBatcherDrainsPendingOnShutdown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	// Create batcher but DON'T start — requests sit in pending map
	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  10 * time.Second,
		MaxTimeout:   10 * time.Second,
		MaxBatchSize: 50,
	})

	req1 := makeTestReq(ctx, "group-a", "item-1")
	req2 := makeTestReq(ctx, "group-a", "item-2")
	b.Enqueue(req1)
	b.Enqueue(req2)

	b.mu.Lock()
	assert.Len(t, b.pendingBatches, 1)
	b.mu.Unlock()

	cancel()
	b.drain()

	for i, req := range []*BatchedRequest[testItem, struct{}]{req1, req2} {
		select {
		case resp := <-req.ResponseChan:
			require.Error(t, resp.Err, "request %d should receive shutdown error", i)
			assert.Contains(t, resp.Err.Error(), "shutting down")
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d timed out", i)
		}
	}
}

func TestBatcherConcurrentRequests(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var callCount int32
	var mu sync.Mutex

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		mu.Lock()
		callCount++ // mutex-protected, not atomic — only accessed under mu
		mu.Unlock()
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  200 * time.Millisecond,
		MaxTimeout:   5 * time.Second,
		MaxBatchSize: 50,
	})
	b.Start()

	const n = 5
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			req := makeTestReq(ctx, "same-group", fmt.Sprintf("item-%d", i))
			select {
			case <-b.Enqueue(req):
			case <-ctx.Done():
			}
		}(i)
	}
	wg.Wait()

	mu.Lock()
	calls := callCount
	mu.Unlock()
	assert.GreaterOrEqual(t, calls, int32(1))
	assert.Less(t, calls, int32(n), "fewer executor calls than requests → batching worked")
}

func TestBatcherMixedKeysConcurrent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	batchKeys := make(map[string]int) // key → batch count

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		mu.Lock()
		batchKeys[batch.Key]++
		mu.Unlock()
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  200 * time.Millisecond,
		MaxTimeout:   5 * time.Second,
		MaxBatchSize: 50,
	})
	b.Start()

	var wg sync.WaitGroup
	// 5 items in group-a, 3 in group-b, 2 in group-c
	groups := []struct {
		group string
		count int
	}{
		{"group-a", 5}, {"group-b", 3}, {"group-c", 2},
	}
	for _, g := range groups {
		for i := 0; i < g.count; i++ {
			wg.Add(1)
			go func(group string, i int) {
				defer wg.Done()
				req := makeTestReq(ctx, group, fmt.Sprintf("item-%d", i))
				select {
				case <-b.Enqueue(req):
				case <-ctx.Done():
				}
			}(g.group, i)
		}
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	// Each group should have been executed at least once
	assert.Contains(t, batchKeys, "group-a")
	assert.Contains(t, batchKeys, "group-b")
	assert.Contains(t, batchKeys, "group-c")
}

func TestBatcherFiresWhenMaxBatchSizeReached(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var batchSizes []int

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		mu.Lock()
		batchSizes = append(batchSizes, len(batch.Requests))
		mu.Unlock()
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
	}, Options{
		IdleTimeout:  10 * time.Second, // very long — should NOT be the trigger
		MaxTimeout:   10 * time.Second, // very long — should NOT be the trigger
		MaxBatchSize: 3,                // this should trigger
	})
	b.Start()

	// Enqueue exactly 3 same-key requests — should fire immediately without waiting for timeout
	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			defer wg.Done()
			req := makeTestReq(ctx, "same-group", fmt.Sprintf("item-%d", i))
			select {
			case <-b.Enqueue(req):
			case <-ctx.Done():
			}
		}(i)
	}

	// Should complete well before the 10s idle/max timeout
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not fire within 2s — MaxBatchSize trigger may be broken")
	}

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, batchSizes, "at least one batch should have fired")
}

func TestBatcherFiresAtMaxTimeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executed := make(chan struct{}, 1)

	b := New[testItem, struct{}](ctx, testKeyFunc, func(batch *Batch[testItem, struct{}]) {
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[struct{}]{Err: nil}
		}
		executed <- struct{}{}
	}, Options{
		IdleTimeout:  10 * time.Second, // very long — idle should NOT fire
		MaxTimeout:   300 * time.Millisecond,
		MaxBatchSize: 1000, // very large — should NOT be the trigger
	})
	b.Start()

	// Enqueue one request, then keep sending more to reset idle timer
	req := makeTestReq(ctx, "group", "item-0")
	b.Enqueue(req)

	// Send requests every 50ms to keep resetting idle timer
	go func() {
		for i := 1; i < 20; i++ {
			time.Sleep(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				return
			default:
				r := makeTestReq(ctx, "group", fmt.Sprintf("item-%d", i))
				b.Enqueue(r)
			}
		}
	}()

	// MaxTimeout is 300ms — batch should fire around then despite ongoing requests
	select {
	case <-executed:
		// good — fired at max timeout
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not fire within 2s — MaxTimeout trigger may be broken")
	}
}
