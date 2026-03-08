# Batch Module - Design Guide

This module groups VM creation requests to reduce Azure API calls. Instead of creating VMs one-by-one, we batch requests with identical configurations into a single API call.

## Why Batching?

When Karpenter needs to create many VMs quickly (e.g., burst of pending pods):
- **Without batching:** N VMs = N API calls (slow, rate-limited, no placement optimization)
- **With batching:** N VMs = 1 API call (faster, efficient, Azure can co-locate)

Think of it like a restaurant kitchen: you *could* cook each dish the moment an order arrives, but it's more efficient to group similar orders together — all steaks on the grill at once, all salads prepped together.

## Architecture

```
                                                ┌─────────────────┐
   Request A (VM size: D4s) ──┐                 │                 │
                              │   ┌──────────┐  │   Coordinator   │
   Request B (VM size: D4s) ──┼──►│ Grouper  │──►│   (executes     │
                              │   │          │  │    batches)     │
   Request C (VM size: D8s) ──┘   └──────────┘  │                 │
                                       │        └─────────────────┘
                                       │
                                  Batch 1: A + B (same template)
                                  Batch 2: C (different template)
```

### Components

| Component | Role |
|-----------|------|
| **Grouper** | Collects requests, groups by template hash, manages timing |
| **Coordinator** | Executes batches against Azure API, distributes results |
| **Types** | Data structures (CreateRequest, PendingBatch, etc.) |
| **Context** | Utilities for passing batch metadata through call stack |

### Data Flow Through Types

```
┌─────────────────┐
│  CreateRequest  │  ← What a caller submits (one per VM needed)
│  (with channel) │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  PendingBatch   │  ← Groups requests with same template hash
│  (N requests)   │
└────────┬────────┘
         │
         ▼
┌──────────────────────┐
│  BatchPutMachine     │  ← HTTP header sent to Azure (JSON)
│  Header              │     Contains per-machine variations
└────────┬─────────────┘
         │
         ▼
┌─────────────────┐
│ CreateResponse  │  ← Sent back via request's channel
└─────────────────┘
```

## How Requests Are Grouped

Requests are grouped by **template hash** - a hash of VM configuration fields that must be identical:

| Category | Fields |
|----------|--------|
| Hardware | VMSize, GPUProfile |
| OS | OSSKU, OSDiskSizeGB, OSDiskType, EnableFIPS |
| Kubernetes | OrchestratorVersion, MaxPods, KubeletConfig |
| Network | VNetSubnetID |
| Scheduling | Priority (Spot vs Regular), Mode |

**Not included** (per-machine variations): MachineName, Zones, Tags — these go in the `BatchPutMachine` HTTP header.

Why hash instead of comparing fields? It's faster and less error-prone. Two VMs with the same hash = same template = can be batched.

## Timing Strategy

The Grouper uses a "wait for idle" strategy with three exit conditions:

```
Timeline:   |-----|-----|-----|-----|-----|-----|-----|
            0    100ms 200ms 300ms 400ms 500ms 600ms

Requests:   R1    R2                R3    R4

IdleTimer:  [====]                        [====]     FIRE! ← Execute
            reset reset                   reset

MaxTimer:   [=====================================]  (still running)
```

1. **Idle timeout:** No new requests for `idleTimeout` → execute (burst ended)
2. **Max timeout:** Waited `maxTimeout` total → execute (latency SLA)
3. **Batch full:** Any batch reaches `maxBatchSize` → execute immediately

### Tuning Tradeoffs

| Small timeouts | Large timeouts |
|----------------|----------------|
| ✓ Low latency | ✓ Better batching |
| ✓ Fast scheduling | ✓ Fewer API calls |
| ✗ More API calls | ✗ Higher latency |

Typical production values:
- `IdleTimeout`: 100-500ms (catch end of burst)
- `MaxTimeout`: 1-5s (latency SLA guarantee)
- `MaxBatchSize`: 10-50 (depends on Azure API limits)

## Key Implementation Details

### Grouper Main Loop

```
😴 Sleep... waiting for trigger
      │
      ▼
🔔 Trigger received! A request arrived
      │
      ▼
⏰ waitForIdle() - collect more requests that might be coming
      │
      ▼
🚀 executeBatches() - dispatch all batches to Coordinator
      │
      └──► repeat forever (until context cancelled)
```

The loop includes panic recovery — if something goes catastrophically wrong, it logs and restarts rather than dying silently.

### Trigger Channel (buffer size 1)

```go
trigger chan struct{} // buffered, size 1
```

This is a Go pattern for "coalescing" notifications:
- If channel is empty → send succeeds, signal delivered
- If channel already has a signal → send is skipped (non-blocking)

Result: 100 rapid enqueues produce at most 1 wakeup. We don't need N signals for N requests — one is enough to wake up the processor.

### Timer Reset Pattern

Go timers are tricky to reset safely. You must:
1. Stop the timer
2. Drain the channel if `Stop()` returns false (timer already fired)
3. Then reset

```go
if !idleTimer.Stop() {
    <-idleTimer.C  // drain to prevent race
}
idleTimer.Reset(g.idleTimeout)
```

This prevents a race where the old timer value is still in the channel.

### Atomic Swap in executeBatches

```go
g.mu.Lock()
batches := g.batches                       // grab current
g.batches = make(map[string]*PendingBatch) // replace with empty
g.mu.Unlock()
// Now process batches without holding lock
```

This is like having two order pads: while the kitchen works on orders from pad A, waiters write new orders on pad B. New requests immediately accumulate in the fresh map — no contention between enqueueing and execution.

### Response Channel Pattern

```go
func EnqueueCreate(req *CreateRequest) chan *CreateResponse
```

Returns a channel instead of blocking. Caller can:
- Continue other work while waiting
- Set up timeout handling
- Handle cancellation gracefully

```
caller                          Grouper                      Coordinator
  │                                │                              │
  │── EnqueueCreate() ────────────►│                              │
  │◄── responseChan ───────────────│                              │
  │                                │                              │
  │ (caller can do other work)     │── (batches more requests) ──►│
  │                                │                              │
  │                                │── ExecuteBatch() ───────────►│
  │                                │                              │
  │◄─────────────────── response sent to channel ─────────────────│
```

### BatchPutMachine Header

Azure batch creation uses a custom HTTP header. The API body contains the shared template; the header contains per-machine variations:

```json
{
  "vmSkus": { "value": [] },
  "batchMachines": [
    { "machineName": "m-abc", "zones": ["1"], "tags": {"team": "platform"} },
    { "machineName": "m-def", "zones": ["2"], "tags": {"team": "platform"} }
  ]
}
```

This allows one API call to create multiple machines with slight variations (different names, zones, tags) while sharing the heavy configuration.

## Flow Diagram

```
Caller
  │
  ├─► EnqueueCreate(req)
  │     │
  │     ├─► computeTemplateHash(template)
  │     ├─► Add to batches[hash]
  │     ├─► trigger <- signal
  │     └─► return req.responseChan
  │
  │   (caller waits on channel)
  │
  │                         Grouper.run() loop
  │                              │
  │                         <─── trigger
  │                              │
  │                         waitForIdle()
  │                              │ (collects more requests)
  │                              │
  │                         executeBatches()
  │                              │
  │                              ▼
  │                         Coordinator.ExecuteBatch()
  │                              │
  │                              ├─► buildBatchHeader()
  │                              ├─► Azure API call (with header)
  │                              ├─► parseFrontendErrors()
  │                              └─► Send to each req.responseChan
  │                                        │
  └──────────────────────────────────────◄─┘
        (caller receives response)
```

## Context Utilities

Go's `context.Context` is like a backpack that travels with a request through the call stack. Instead of passing batch info as parameters through 10 layers of functions, we attach it to the context:

```
┌─────────────┐
│   Caller    │  ctx = context.Background()
└──────┬──────┘
       │
       ▼
┌──────────────────┐
│   Grouper        │  // Receives and batches requests
└──────┬───────────┘
       │
       ▼
┌──────────────────┐
│   Coordinator    │  ctx = WithBatchMetadata(ctx, &BatchMetadata{...})
└──────┬───────────┘
       │
       ▼
┌──────────────────┐
│  Azure Client    │  // Can call FromContext(ctx) to get batch info
└──────┬───────────┘
       │
       ▼
┌──────────────────┐
│   HTTP Layer     │  // Also has access to batch metadata if needed
└──────────────────┘
```

### Usage

```go
// After batch execution, mark the context
ctx = batch.WithBatchMetadata(ctx, &BatchMetadata{
    IsBatched:   true,
    MachineName: "m-abc",
    BatchID:     "uuid-123",
})

// Downstream code can check
if meta := batch.FromContext(ctx); meta != nil {
    log.Info("batched", "batchID", meta.BatchID)
}

// Skip batching for special cases (retries, urgent requests, testing)
ctx = batch.WithSkipBatching(ctx)
if batch.ShouldSkipBatching(ctx) {
    // Use direct API call instead
}
```

## Azure SDK Notes

- **Poller pattern:** Azure VM creation is a long-running operation (LRO). The API returns immediately with a Poller, which you use to check status and get the final result.
- **Pointer types:** Azure SDK uses pointers everywhere for optional fields. The `extractZones` and `extractTags` helpers convert these to concrete values for JSON serialization.
