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

// Package batcher provides a generic request-coalescing framework.
//
// It collects incoming requests, groups them by a caller-defined key, waits
// for a configurable idle/max timeout, then dispatches each group to a
// caller-defined executor. Callers block on a per-request response channel
// until the batch fires.
//
// Type parameters:
//   - RequestPayload: the original request payload type (e.g., an AKS machine body for creation)
//   - ResponsePayload: the response type returned to each request (e.g., a poller for async operations, or struct{} if unused)
package batcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Batch is a group of requests with the same key.
type Batch[RequestPayload, ResponsePayload any] struct {
	ID       string
	Key      string
	Requests []*BatchedRequest[RequestPayload, ResponsePayload]
}

// BatchedRequest is a single request (w/ payload) being batched with others.
type BatchedRequest[RequestPayload, ResponsePayload any] struct {
	// Warning: the batcher does not currently honor per-request context cancellation.
	// Once a request is enqueued, it stays in the batch even if the caller's context is canceled.
	// Suggestion: support per-request cancellation guarantee, only if needed.

	Key          string                          // Grouping key, set by the batcher after calling DetermineBatchKey
	ResponseChan chan *Response[ResponsePayload] // Caller waits on this channel for the response after batch execution

	Payload RequestPayload // The original request payload (e.g., an AKS machine body for creation)
}

// Response is used by ExecuteBatch to send a response to each original request.
type Response[ResponsePayload any] struct {
	Payload ResponsePayload // The response payload to send back to the caller (e.g., a poller for async operations and API errors)
	Err     error           // Operational error (e.g., batch execution failure) to send back to the caller
}

// DetermineBatchKey computes a grouping key from a payload that will be batched from.
// Payloads with the same key land in the same batch.
// The caller module must provide this.
type DetermineBatchKey[RequestPayload any] func(payload *RequestPayload) (string, error)

// ExecuteBatch is called when a batch fires by the batcher. It receives the batch
// and must send a response to every request's ResponseChan.
// The caller module must provide this.
type ExecuteBatch[RequestPayload, ResponsePayload any] func(ctx context.Context, batch *Batch[RequestPayload, ResponsePayload])

// Options configures the batching behavior.
//
// Small timeouts = lower latency, more API calls.
// Large timeouts = better batching, higher latency.
type Options struct {
	// IdleTimeout is how long to wait with no new requests before firing the batch.
	IdleTimeout time.Duration
	// MaxTimeout is the maximum time a batch can remain open regardless of activity.
	MaxTimeout time.Duration
	// MaxBatchSize causes a batch to fire immediately once it reaches this many requests.
	MaxBatchSize int
}

// Batcher collects requests, groups them by key, and dispatches batches
// after a configurable idle/max timeout window.
type Batcher[RequestPayload, ResponsePayload any] struct {
	ctx            context.Context
	mu             sync.Mutex
	pendingBatches map[string]*Batch[RequestPayload, ResponsePayload] // Store pending batches to be executed
	trigger        chan struct{}                                      // Alert the background loop when new requests arrive; buffered at 1 so rapid enqueues coalesce into a single wakeup.

	determineBatchKey DetermineBatchKey[RequestPayload]
	executeBatch      ExecuteBatch[RequestPayload, ResponsePayload]

	opts Options
}

// New creates a Batcher with configured behavior. Call Start() to begin processing loop.
func New[RequestPayload, ResponsePayload any](
	ctx context.Context,
	determineBatchKeyFunc DetermineBatchKey[RequestPayload],
	executeBatchFunc ExecuteBatch[RequestPayload, ResponsePayload],
	opts Options,
) *Batcher[RequestPayload, ResponsePayload] {
	return &Batcher[RequestPayload, ResponsePayload]{
		ctx:               ctx,
		pendingBatches:    make(map[string]*Batch[RequestPayload, ResponsePayload]),
		trigger:           make(chan struct{}, 1),
		determineBatchKey: determineBatchKeyFunc,
		executeBatch:      executeBatchFunc,
		opts:              opts,
	}
}

// Start launches the background processing loop.
func (b *Batcher[RequestPayload, ResponsePayload]) Start() {
	go b.run()
}

// Enqueue adds a request to the appropriate batch and returns a response channel.
// The caller should select on the channel and ctx.Done().
func (b *Batcher[RequestPayload, ResponsePayload]) Enqueue(payload RequestPayload) (chan *Response[ResponsePayload], error) {
	key, err := b.determineBatchKey(&payload)
	if err != nil {
		return nil, fmt.Errorf("failed to determine batch key: %w", err)
	}
	req := &BatchedRequest[RequestPayload, ResponsePayload]{
		Payload:      payload,
		ResponseChan: make(chan *Response[ResponsePayload], 1),
		Key:          key,
	}

	b.mu.Lock()

	batch, exists := b.pendingBatches[req.Key]
	if !exists {
		// First request for this key → need to initialize batch first
		batch = &Batch[RequestPayload, ResponsePayload]{
			ID:       uuid.New().String(),
			Key:      req.Key,
			Requests: make([]*BatchedRequest[RequestPayload, ResponsePayload], 0, b.opts.MaxBatchSize),
		}
		b.pendingBatches[req.Key] = batch
	}
	batch.Requests = append(batch.Requests, req)

	b.mu.Unlock()

	// Alert the background loop (e.g., start timer, check execution conditions)
	// Non-blocking signal (buffer=1 coalesces multiple enqueues)
	select {
	case b.trigger <- struct{}{}:
	default:
	}

	// Return the channel the caller should wait on.
	// The channel will receive the batch response once the batch fires and executeBatch is done.
	return req.ResponseChan, nil
}

// Main loop: keep collecting requests → wait for trigger → execute batches → repeat.
func (b *Batcher[RequestPayload, ResponsePayload]) run() {
	defer b.drain()

	for {
		select {
		case <-b.ctx.Done():
			return

		case <-b.trigger:
			batcherIterationID := uuid.New().String()
			waitStartTime := time.Now()
			// Woken up, as there's a new request and enqueuement. Then:
			b.waitForIdle()
			if b.ctx.Err() != nil {
				return // batcher context canceled, drain
			}
			// Note: the timing window is shared across all batch keys. A late-arriving
			// request for key B resets the idle timer even if key A's batch was already
			// "ready." MaxTimeout bounds the total wait.
			// Execution also fires for all batches at once from that shared timer.
			// This is tolerable because requests typically arrive in bursts from the provisioner.
			// Suggestion: if needed, we could add per-batch-key timers for more precise control, but it adds complexity.

			// TODO: use metrics instead?
			log.FromContext(b.ctx).V(2).Info("batcher iteration finishing wait, ready to execute batches",
				"batcherIterationID", batcherIterationID,
				"waitStartTime", waitStartTime,
				"waitDuration", time.Since(waitStartTime),
				"batchCount", len(b.pendingBatches))
			b.executeBatches(batcherIterationID)
		}
	}
}

// waitForIdle blocks until it's time to execute batches. Returns when:
//  1. idleTimeout passes with no new requests (burst ended)
//  2. maxTimeout passes (latency SLA)
//  3. Any batch reaches maxBatchSize (full batch)
func (b *Batcher[RequestPayload, ResponsePayload]) waitForIdle() {
	maxTimer := time.NewTimer(b.opts.MaxTimeout)
	idleTimer := time.NewTimer(b.opts.IdleTimeout)
	defer maxTimer.Stop()
	defer idleTimer.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return

		case <-b.trigger:
			// More request arrived and its enqueuement occurred.

			if b.anyBatchFull() {
				return
			}

			// Reset idle timer
			if !idleTimer.Stop() {
				// Timer is over, but we don't care and still need to reset the timer.
				// Need draining to prevent the leaky fire, even after reset.
				// See Stop() doc for more details.
				<-idleTimer.C
			}
			idleTimer.Reset(b.opts.IdleTimeout)

		case <-idleTimer.C:
			return
		case <-maxTimer.C:
			return
		}
	}
}

// anyBatchFull returns true if any pending batch has reached MaxBatchSize.
func (b *Batcher[RequestPayload, ResponsePayload]) anyBatchFull() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, batch := range b.pendingBatches {
		if len(batch.Requests) >= b.opts.MaxBatchSize {
			return true
		}
	}
	return false
}

// executeBatches atomically swaps out the batch map and dispatches all batches.
func (b *Batcher[RequestPayload, ResponsePayload]) executeBatches(batcherIterationID string) {
	// Atomically swaps out the batch map.
	b.mu.Lock()
	batches := b.pendingBatches
	b.pendingBatches = make(map[string]*Batch[RequestPayload, ResponsePayload])
	b.mu.Unlock()

	// Dispatch batches in parallel, as they are independent (different keys).
	for _, batch := range batches {
		// TODO: use metrics instead?
		log.FromContext(b.ctx).V(2).Info("begin executing batch",
			"batcherIterationID", batcherIterationID,
			"ID", batch.ID,
			"key", batch.Key,
			"size", len(batch.Requests))
		go func(batch *Batch[RequestPayload, ResponsePayload]) {
			defer func() {
				if r := recover(); r != nil {
					log.FromContext(b.ctx).Error(fmt.Errorf("%v", r), "panic in batch executor, distributing error to callers")
					err := fmt.Errorf("batch execution panicked: %v", r)
					for _, req := range batch.Requests {
						req.ResponseChan <- &Response[ResponsePayload]{Err: err}
					}
				}
			}()

			b.executeBatch(b.ctx, batch)
		}(batch)
	}
}

// drain fails all in-flight requests with a shutdown error.
func (b *Batcher[RequestPayload, ResponsePayload]) drain() {
	b.mu.Lock()
	batches := b.pendingBatches
	b.pendingBatches = make(map[string]*Batch[RequestPayload, ResponsePayload])
	b.mu.Unlock()

	shutdownErr := fmt.Errorf("batcher shutting down")
	drained := 0
	for _, batch := range batches {
		for _, req := range batch.Requests {
			req.ResponseChan <- &Response[ResponsePayload]{Err: shutdownErr}
			drained++
		}
	}

	if drained > 0 {
		log.FromContext(b.ctx).V(2).Info("batcher drained pending requests on shutdown",
			"drainedRequests", drained)
	}
}
