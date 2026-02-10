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
// Self-heals on panic by restarting.
func (g *Grouper) run() {
	defer func() {
		if r := recover(); r != nil {
			log.FromContext(g.ctx).Error(fmt.Errorf("%v", r), "BatchGrouper panic, restarting")
			g.run()
		}
	}()

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
		go g.coordinator.ExecuteBatch(batch) //nolint:errcheck // errors are delivered to each request's response channel
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
// Batched together: VMSize, OS settings, Kubernetes config, network, priority, mode.
// NOT batched (per-machine): machine name, zones, tags—these go in the BatchPutMachine header.
func computeTemplateHash(template *armcontainerservice.Machine) string {
	if template == nil || template.Properties == nil {
		return ""
	}

	type templateKey struct {
		VMSize              *string
		Priority            *armcontainerservice.ScaleSetPriority
		OrchestratorVersion *string
		OSSKU               *armcontainerservice.OSSKU
		OSDiskSizeGB        *int32
		OSDiskType          *armcontainerservice.OSDiskType
		EnableFIPS          *bool
		MaxPods             *int32
		VNetSubnetID        *string
		KubeletConfig       *armcontainerservice.KubeletConfig
		GPUProfile          *armcontainerservice.GPUProfile
		Mode                *armcontainerservice.AgentPoolMode
	}

	props := template.Properties
	key := templateKey{
		Priority: props.Priority,
		Mode:     props.Mode,
	}

	if props.Network != nil {
		key.VNetSubnetID = props.Network.VnetSubnetID
	}
	if props.Hardware != nil {
		key.VMSize = props.Hardware.VMSize
		key.GPUProfile = props.Hardware.GpuProfile
	}
	if props.OperatingSystem != nil {
		key.OSSKU = props.OperatingSystem.OSSKU
		key.OSDiskSizeGB = props.OperatingSystem.OSDiskSizeGB
		key.OSDiskType = props.OperatingSystem.OSDiskType
		key.EnableFIPS = props.OperatingSystem.EnableFIPS
	}
	if props.Kubernetes != nil {
		key.OrchestratorVersion = props.Kubernetes.OrchestratorVersion
		key.MaxPods = props.Kubernetes.MaxPods
		key.KubeletConfig = props.Kubernetes.KubeletConfig
	}

	jsonBytes, _ := json.Marshal(key)
	hash := sha256.Sum256(jsonBytes)
	return fmt.Sprintf("%x", hash[:8])
}
