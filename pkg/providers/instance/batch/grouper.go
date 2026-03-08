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

/*
Package batch groups VM creation requests to reduce Azure API calls.

Instead of creating VMs one-by-one, the Grouper collects requests with identical
configurations and sends them as a single batched API call. This improves
throughput, reduces rate limiting, and lets Azure optimize placement.

Flow:

	Requests ──► Grouper (groups by template hash) ──► Coordinator (executes batch)
	                  │
	             Batch 1: VMs with same config
	             Batch 2: VMs with different config
*/
package batch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Grouper collects VM creation requests and groups them by template for batch execution.
//
// It runs a background loop that waits for requests, gives them time to accumulate,
// then dispatches batches to the Coordinator. Requests with identical VM configs
// (same size, OS, network, etc.) land in the same batch.
type Grouper struct {
	ctx     context.Context
	enabled bool

	mu      sync.Mutex
	batches map[string]*PendingBatch // templateHash → pending requests

	// Buffered channel (size 1) for coalescing signals. Multiple rapid enqueues
	// produce at most one wakeup—we don't need N signals for N requests.
	trigger chan struct{}

	coordinator  *Coordinator
	idleTimeout  time.Duration // Wait this long after last request before executing
	maxTimeout   time.Duration // Maximum wait time (latency SLA)
	maxBatchSize int           // Execute immediately when a batch reaches this size
}

// Options tunes the batching behavior.
//
//	Small timeouts = lower latency, more API calls
//	Large timeouts = better batching, higher latency
type Options struct {
	IdleTimeout  time.Duration
	MaxTimeout   time.Duration
	MaxBatchSize int
}

// NewGrouper creates a Grouper. Call SetCoordinator() then Start() to begin processing.
func NewGrouper(ctx context.Context, opts Options) *Grouper {
	return &Grouper{
		ctx:          ctx,
		enabled:      true,
		batches:      make(map[string]*PendingBatch),
		trigger:      make(chan struct{}, 1),
		idleTimeout:  opts.IdleTimeout,
		maxTimeout:   opts.MaxTimeout,
		maxBatchSize: opts.MaxBatchSize,
	}
}

func (g *Grouper) SetCoordinator(coordinator *Coordinator) {
	g.coordinator = coordinator
}

func (g *Grouper) IsEnabled() bool {
	return g.enabled
}

// Start launches the background processing loop.
func (g *Grouper) Start() {
	go g.run()
}

// run is the main loop: wait for trigger → collect more requests → execute batches → repeat.
// On exit, drains all pending requests with an error so callers don't block.
//
// No panic recovery here: if the loop panics, it's a programmer error that should crash
// the process for immediate detection. Per-batch recovery in executeBatches() isolates
// individual batch failures so they don't take down the loop.
func (g *Grouper) run() {
	defer g.drainPendingRequests()

	for {
		select {
		case <-g.ctx.Done():
			return
		case <-g.trigger:
			g.waitForIdle()
			g.executeBatches()
		}
	}
}

// waitForIdle blocks until it's time to execute batches. Exits when:
//  1. idleTimeout passes with no new requests (burst ended)
//  2. maxTimeout passes (latency SLA)
//  3. Any batch reaches maxBatchSize (full batch)
func (g *Grouper) waitForIdle() {
	maxTimer := time.NewTimer(g.maxTimeout)
	idleTimer := time.NewTimer(g.idleTimeout)
	defer maxTimer.Stop()
	defer idleTimer.Stop()

	for {
		select {
		case <-g.ctx.Done():
			return

		case <-g.trigger:
			// New request arrived. Check if any batch is full.
			g.mu.Lock()
			anyFull := false
			for _, batch := range g.batches {
				if len(batch.requests) >= g.maxBatchSize {
					anyFull = true
					break
				}
			}
			g.mu.Unlock()

			if anyFull {
				return
			}

			// Reset idle timer (proper Go timer reset: stop, drain, reset)
			if !idleTimer.Stop() {
				<-idleTimer.C
			}
			idleTimer.Reset(g.idleTimeout)

		case <-idleTimer.C:
			return // No activity for idleTimeout
		case <-maxTimer.C:
			return // Hit max wait time
		}
	}
}

// executeBatches atomically swaps out the batch map and dispatches all batches.
// New requests immediately start accumulating in a fresh map (no contention).
func (g *Grouper) executeBatches() {
	g.mu.Lock()
	batches := g.batches
	g.batches = make(map[string]*PendingBatch)
	g.mu.Unlock()

	for _, batch := range batches {
		go func(b *PendingBatch) {
			defer func() {
				if r := recover(); r != nil {
					log.FromContext(g.ctx).Error(fmt.Errorf("%v", r), "panic in ExecuteBatch, distributing error to callers")
					g.coordinator.distributeError(b, fmt.Errorf("batch execution panicked: %v", r))
				}
			}()
			g.coordinator.ExecuteBatch(b)
		}(batch)
	}
}

// drainPendingRequests fails all in-flight requests with a shutdown error.
// Called when the Grouper's run loop exits (context cancellation or panic limit).
// Without this, callers would block on their response channels until their
// individual context timeouts, creating a latency cliff during deployments.
func (g *Grouper) drainPendingRequests() {
	g.mu.Lock()
	batches := g.batches
	g.batches = make(map[string]*PendingBatch)
	g.mu.Unlock()

	shutdownErr := fmt.Errorf("batch grouper shutting down")
	drained := 0
	for _, batch := range batches {
		for _, req := range batch.requests {
			req.responseChan <- &CreateResponse{Poller: nil, Err: shutdownErr}
			drained++
		}
	}

	if drained > 0 {
		log.FromContext(g.ctx).Info("BatchGrouper drained pending requests on shutdown",
			"drainedRequests", drained)
	}
}

// EnqueueCreate adds a request to the appropriate batch and returns a channel
// for the response. The caller should wait on the returned channel.
func (g *Grouper) EnqueueCreate(req *CreateRequest) chan *CreateResponse {
	templateHash := computeTemplateHash(&req.template)

	g.mu.Lock()
	batch, exists := g.batches[templateHash]
	if !exists {
		batch = &PendingBatch{
			templateHash: templateHash,
			template:     req.template,
			requests:     make([]*CreateRequest, 0, g.maxBatchSize),
		}
		g.batches[templateHash] = batch
	}
	batch.requests = append(batch.requests, req)
	g.mu.Unlock()

	// Non-blocking signal (buffer=1 coalesces multiple enqueues)
	select {
	case g.trigger <- struct{}{}:
	default:
	}

	return req.responseChan
}

// computeTemplateHash generates a hash of VM config fields that must match for batching.
//
// Approach: copy MachineProperties, zero out per-machine and read-only fields,
// then JSON-marshal the remainder. This ensures any new field added to
// MachineProperties is automatically included in the hash (fail-safe).
// See TestComputeTemplateHash_AllFieldsAccountedFor for a reflection-based
// guardrail that catches unaccounted-for fields at test time.
//
// Excluded from hash (per-machine, travel via BatchPutMachine header):
//   - Tags: contains NodeClaim name and creation timestamp (unique per machine)
//
// Excluded from hash (read-only, set by server):
//   - ETag, ProvisioningState, ResourceID, Status
//
// Note: Machine.Zones and Machine.Name are also per-machine but live on Machine,
// not MachineProperties, so they are not part of this hash.
func computeTemplateHash(template *armcontainerservice.Machine) string {
	if template == nil || template.Properties == nil {
		return ""
	}

	// Shallow copy so we can zero out excluded fields without mutating the original.
	props := *template.Properties

	// Per-machine fields (travel via BatchPutMachine header, unique per machine)
	props.Tags = nil

	// Read-only fields (set by server, never part of the template)
	props.ETag = nil
	props.ProvisioningState = nil
	props.ResourceID = nil
	props.Status = nil

	jsonBytes, err := json.Marshal(props)
	if err != nil {
		// If marshaling fails, fall back to fmt.Sprintf which is slower but always works.
		// This prevents different templates from colliding on the same (empty) hash.
		jsonBytes = []byte(fmt.Sprintf("%+v", props))
	}
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash[:8])
}
