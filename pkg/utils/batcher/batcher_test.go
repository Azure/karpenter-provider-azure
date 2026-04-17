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

	"github.com/onsi/gomega"
)

// testItem is a simple item type for testing.
type testItem struct {
	Group string // used as the grouping key
	Name  string // per-item identifier
}

func testKeyFunc(item *testItem) string {
	return item.Group
}

func TestBatcherEnqueue(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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

	ch := b.Enqueue(testItem{Group: "group-a", Name: "item-1"})
	g.Expect(ch).ToNot(gomega.BeNil())

	// Verify the request is in the internal map
	b.mu.Lock()
	g.Expect(b.pendingBatches).To(gomega.HaveLen(1), "should have one pending batch")
	b.mu.Unlock()
}

func TestBatcherGroupsSameKey(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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

	b.Enqueue(testItem{Group: "group-a", Name: "item-1"})
	b.Enqueue(testItem{Group: "group-a", Name: "item-2"})

	b.mu.Lock()
	g.Expect(b.pendingBatches).To(gomega.HaveLen(1), "same key → one batch")
	for _, batch := range b.pendingBatches {
		g.Expect(batch.Requests).To(gomega.HaveLen(2), "batch should contain both requests")
	}
	b.mu.Unlock()
}

func TestBatcherSeparatesDifferentKeys(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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

	b.Enqueue(testItem{Group: "group-a", Name: "item-1"})
	b.Enqueue(testItem{Group: "group-b", Name: "item-2"})

	b.mu.Lock()
	g.Expect(b.pendingBatches).To(gomega.HaveLen(2), "different keys → two batches")
	b.mu.Unlock()
}

func TestBatcherDrainsPendingOnShutdown(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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

	ch1 := b.Enqueue(testItem{Group: "group-a", Name: "item-1"})
	ch2 := b.Enqueue(testItem{Group: "group-a", Name: "item-2"})

	b.mu.Lock()
	g.Expect(b.pendingBatches).To(gomega.HaveLen(1))
	b.mu.Unlock()

	cancel()
	b.drain()

	for i, ch := range []chan *Response[struct{}]{ch1, ch2} {
		select {
		case resp := <-ch:
			g.Expect(resp.Err).To(gomega.HaveOccurred(), "request %d should receive shutdown error", i)
			g.Expect(resp.Err.Error()).To(gomega.ContainSubstring("shutting down"))
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d timed out", i)
		}
	}
}

func TestBatcherConcurrentRequests(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
			select {
			case <-b.Enqueue(testItem{Group: "same-group", Name: fmt.Sprintf("item-%d", i)}):
			case <-ctx.Done():
			}
		}(i)
	}
	wg.Wait()

	mu.Lock()
	calls := callCount
	mu.Unlock()
	g.Expect(calls).To(gomega.BeNumerically(">=", int32(1)))
	g.Expect(calls).To(gomega.BeNumerically("<", int32(n)), "fewer executor calls than requests → batching worked")
}

func TestBatcherMixedKeysConcurrent(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
	for _, grp := range groups {
		for i := 0; i < grp.count; i++ {
			wg.Add(1)
			go func(group string, i int) {
				defer wg.Done()
				select {
				case <-b.Enqueue(testItem{Group: group, Name: fmt.Sprintf("item-%d", i)}):
				case <-ctx.Done():
				}
			}(grp.group, i)
		}
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	// Each group should have been executed at least once
	g.Expect(batchKeys).To(gomega.HaveKey("group-a"))
	g.Expect(batchKeys).To(gomega.HaveKey("group-b"))
	g.Expect(batchKeys).To(gomega.HaveKey("group-c"))
}

func TestBatcherFiresWhenMaxBatchSizeReached(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)
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
			select {
			case <-b.Enqueue(testItem{Group: "same-group", Name: fmt.Sprintf("item-%d", i)}):
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
	g.Expect(batchSizes).ToNot(gomega.BeEmpty(), "at least one batch should have fired")
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
	b.Enqueue(testItem{Group: "group", Name: "item-0"})

	// Send requests every 50ms to keep resetting idle timer
	go func() {
		for i := 1; i < 20; i++ {
			time.Sleep(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				return
			default:
				b.Enqueue(testItem{Group: "group", Name: fmt.Sprintf("item-%d", i)})
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
