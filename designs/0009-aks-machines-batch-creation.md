# Batch Creation for AKS Machines

**Author:** @comtalyst

**Last updated:** Apr 16, 2026

**Status:** Completed

## Overview

Karpenter/NAP is scaling: it is going to support provisioning a large number of nodes around the same time (e.g., to handle large bursts of user workloads), let's say, `N` nodes.

However, creating `N` instances via `N` individual API calls increases server-side load, not to mention the per-call overhead.
Current server-side capacity has resulted in necessary server-side throttling of Karpenter, hurting provisioning performance after a certain `N`.

Given the logical redundancy of `N` individual API calls, batching them into fewer calls is a significant opportunity for Karpenter to pursue.

### Goals

* Batch multiple AKS Machine creates into one API call.
* Configurable grouping parameters (idle timeout, max timeout, max batch size).

### Non-Goals

* A fleet-style API, a formal API for batch creation, or ARM/SDK-owned batching mechanism
  * This would delegate the ownership of generic Azure resource batching away, resulting in a more efficient dev flow, higher reliability, etc. Not yet available for this iteration.
* Batching deletes or updates
  * This is less prioritized due to lower demands
* Batching of non-AKS-machine instances (i.e., for provision modes other than `aksmachineapi`)
  * AKS machine API path is intentionally prioritized (see 0007/0008 design docs)
  * Still, flow control logic (e.g., the "generic batcher") may be reusable later on

## Design

### Batch creation in the current AKS machine API

In the current phase, AKS machine API provides an informal header to support batching multiple creates in a single `PUT` request. To demonstrate:

```json
PUT /machines/machine1
Body:   { "vmSize": "Standard_D2s_v3", ... }
Header: { "batchMachines": [
  { "machineName": "machine1", "zones": ["1"], "tags": {"nodeclaim": "nodeclaim1"} },
  { "machineName": "machine2", "zones": ["2"], "tags": {"nodeclaim": "nodeclaim2"} },
  { "machineName": "machine3", "zones": ["3"], "tags": {"nodeclaim": "nodeclaim3"} }
]}
```

Would result in

| Machine | Tags | Zone | Others |
|---------|---------------------|------|------|
| m-1 | `{"nodeclaim": "nodeclaim1"}` | `1` | Standard_D2s_v3, ...from shared body |
| m-2 | `{"nodeclaim": "nodeclaim2"}` | `2` | Standard_D2s_v3, ...from shared body |
| m-3 | `{"nodeclaim": "nodeclaim3"}` | `3` | Standard_D2s_v3, ...from shared body |

Machine name, tags, and zone are selected as machine-unique fields. Other fields are more likely to be uniform in a typical burst, thus carried once in the shared body. Zone/Tags fields in the shared body will be discarded. The maximum number of machines per batch is 50.

#### Errors

If errors occur before creation starts server-side, they will be returned in a list of failed machines and errors. TODO: exact format.

#### Updates?

The operation also does not support update, so operations on existing machines will be considered an error.

#### Poller?

However, the poller for the asynchronous operation returned by the current API from the SDK does not cover multiple machines, thus being unusable, as Karpenter needs to monitor the status and handle errors.

### Karpenter integration: enablement

A new provision mode `ProvisionModeAKSMachineAPIHeaderBatch  = "aksmachineapiheaderbatch"` is introduced. This will result in all AKS machine creates going through the batch code path at a certain point, which will be described in the sections below.

#### No auto-select?

Whether to batch or not to batch is currently determined through that global option. Automatically determining whether a request should be batched or not is an additional complexity, and is currently out-of-scope. This can also be revisited if performance for smaller provisioning cases is degraded significantly.

### Divergence from non-batch scenario

* Without batch, Karpenter creates an individual AKS machine API instance through `pkg/providers/azclient/azapi.AKSMachinesAPI.BeginCreateOrUpdate` interface from `pkg/providers/instance/aksmachineinstance`.
  * Azure SDK implements this interface directly
* With batch, we introduce a new interface `AKSMachinesHeaderBatchAPI.BeginCreateWithBatch` and "AKS machines header batch client" in `pkg/providers/azclient/aksmachinesheaderbatch`

The general expectations for both are similar: **the call returns upon ensuring that the creation of the specified machine template has started, server-side.**
However, `AKSMachinesHeaderBatchAPI.BeginCreateWithBatch` does not support update. If some machines in the header already exist (which can be interpreted as an update), they will fail.

In addition, due to the poller limitations noted earlier, the batching path uses the GET-based poller (`pkg/providers/instance/aksmachinepoller`) to poll each machine individually. A separate interface has been created to make these differences in expectations clear.

In this layer, both batch and non-batch share the same error handling logic. More details in "error handling" section below.

The divergence is handled with a simple if statement like below.

```go
if batchCreationEnabled {
    // Batch path: AKSMachinesHeaderBatchAPI.BeginCreateWithBatch → GET poller
} else {
    // Non-batch path: AKSMachinesAPI.BeginCreateOrUpdate → SDK poller
}
```

### AKS machines header batch client

The "AKS machines header batch client" mentioned earlier utilizes a generic batcher from `pkg/utils/batcher` behind the scenes.

On `BeginCreateWithBatch`, the request is enqueued into the generic batcher, which returns a response channel to watch. The caller then blocks on this response channel.

The batcher handles the grouping logic and request submission. Once the response is delivered through the response channel, `BeginCreateWithBatch` unblocks and returns, with or without an error. The SDK-returned poller is discarded at this point.

### Generic batcher

`pkg/utils/batcher` — generic request-coalescing framework. See the code for lower-level details (e.g., lock acquisition).

The caller (`BeginCreateWithBatch`) keeps calling `Enqueue` to submit requests. `Enqueue` groups each request by a caller-defined key computation function, and ensures the timer is active.

The timer dispatches batches when any of these conditions are met:

* **Idle timeout**: no new request arrived for N ms → burst ended, fire.
* **Max timeout**: the batch has been open for N ms → latency SLA, fire.
* **Max batch size**: any batch reached the configured limit → fire immediately.

All three parameters are caller-configured via `batcher.Options`:

| Parameter | Env Var | Effect |
|-----------|---------|---------|
| Idle timeout | `BATCH_IDLE_TIMEOUT_MS` | How long to wait after last request before firing |
| Max timeout | `BATCH_MAX_TIMEOUT_MS` | Hard cap on batch wait time |
| Max batch size | `MAX_BATCH_SIZE` | Maximum machines to be created in one batch (must <= server-side limit) |

#### Shared timer across keys

The timing window is shared across all batch keys.
A late-arriving request for key B resets the idle timer even if key A's batch was already "ready." MaxTimeout bounds the total wait; this is acceptable because requests typically arrive in bursts from the provisioner.

Ideally, per-batch-key timers could provide more precise control at the cost of complexity — a future improvement if needed.

#### Dispatch

When the window closes, the batcher atomically swaps the pending batch map with a fresh empty one (so new requests accumulate immediately without contention), then dispatches each batch to the executor in a separate goroutine.
Each goroutine has panic recovery — if an executor panics, the affected batch's callers receive an error, but other batches and the main loop are unaffected.

### Batch key

#### Hashing

`aksmachinesheaderbatch/batchkey.go` computes the grouping key from the resource path (`rg/cluster/pool`) and a SHA-256 hash of the shared `MachineProperties` (after clearing per-machine and read-only fields) to determine whether two machines can share a batch.
This function is used by the generic batcher to assign requests to batches. Same key means same batch.

#### Per-machine fields exclusion

Per-machine fields (currently only `Tags`) and read-only fields (`ETag`, `ProvisioningState`, `ResourceID`, `Status`) are cleared before hashing so that machines differing only in these fields batch together. `Machine.Zones` and `Machine.Name` are also per-machine but live on the `Machine` struct (not `MachineProperties`), so they're excluded from hashing by virtue of not being in the hashed struct.

### Batch execution

`aksmachinesheaderbatch/executor.go` — called by the batcher when a batch fires. It:

1. Builds per-machine `MachineEntry` data (name, zones, tags) from each request, then constructs the `BatchPutMachine` HTTP header (JSON with per-machine entries).
2. Calls `AKSMachinesCreateAPI.BeginCreateOrUpdate` (a narrow consumer-side view of the SDK's `MachinesClient`, defined in `aksmachinesheaderbatch`) with the header and template.
3. If the call returns an error, the executor distributes that error to each request's response channel.
4. Otherwise, the returned SDK poller is discarded and a nil error is returned to the response channel.
5. The caller (`client.BeginCreateBatch`) receives the response/error, then returns it to the instance provider.

### Error handling

#### Original request context cancellation

If the original request context is cancelled after the creation has been enqueued for a batched request, that cancellation will not be honored unless the batch's background context is cancelled. This gap is acceptable as instance garbage collection will lead to eventual consistency, and the likelihood that this corner case occurs is low.

If this is proven to be a significant gap, a solution can be proposed separately.

#### Synchronous phase/batch API call error

Currently, an error in the batched request is treated as if every individual create in the batch had failed — the same as the non-batch case. There is no error-handling logic (e.g., quota) in this phase in either batch or non-batch. Thus, they share the same failure mode (e.g., clean-up, then retry if not timed out). TODO: per-machine error (WIP)

#### Asynchronous phase error

Once the synchronous-phase batched request is completed, each instance creation routine goes into the async phase (with GET-poller); provisioning errors and completion are handled the same way as in the non-batch case, except that status polling is done via the GET-poller (which calls GET machine) instead of the SDK poller (which calls GET operation).

### GET-poller

A.k.a. `aksmachinepoller`. There will be one poller instance per creating machine instance. At this point, it is unrelated to batching; it is a mitigation for the issue of the AKS machines header batch API not having a suitable poller.

The poller is a best-effort imitation of the SDK poller, but swaps the GET operation call with a GET machine call.

It polls periodically and reads the machine's provisioning state:

| Provisioning State | Poller Action |
|-------------------|---------------|
| `Creating` / `Updating` | Continue polling. |
| `Succeeded` | Return success. |
| `Failed` | Extract `ProvisioningError` from the machine object and return it. |
| `Deleting` | Return error — machine was deleted mid-provision. |

Once polling is done, the rest can be handled the same way as the non-batch/SDK poller case.

#### GET machine throttling?

A separate proposal on potentially using LIST-based poller/caching/new endpoint is being considered to combat GET machine throttling.

### Flow example

For example, when Karpenter needs to create 5 machines with the same config:

1. The provisioner creates 5 NodeClaims. Core Karpenter calls `CloudProvider.Create()` for each, in parallel.
2. Each `Create()` reaches `beginCreateMachine()`, which calls `batchClient.BeginCreateWithBatch()`. This enqueues the request into the batcher and **blocks** on a response channel.
3. The batcher groups all 5 requests under the same key (same config = same hash). After the idle timeout passes with no new requests, the batcher fires.
4. The executor builds a single `BatchPutMachine` HTTP header with per-machine entries (names, zones, tags), clears per-machine fields from the template body, and calls `AKSMachinesCreateAPI.BeginCreateOrUpdate` once.
5. AKS Machine API begins creating all 5 machines and returns. The executor sends a success response to each of the 5 blocked callers via their response channels.
   * If the API returns an error, the executor distributes it to all 5 callers. Each caller follows the error handling path.
6. Each caller unblocks, does a GET to retrieve the machine's details, then starts polling via the GET-based poller for provisioning completion. At this point, 5 individual GET pollers are running — each ticking every 5 seconds. This is where batching shifts load from PUTs to GETs: 1 PUT was sent, but 5 × (provisioning duration / 5s) GETs follow.
7. Response/error handling beyond this point is shared with the non-batch case.

### Future migrations

Once a different form (e.g., more formal w/o headers, or fleet-style) of batch API is finally available, the generic batcher (`pkg/utils/batcher`) can be shared, while the new code path introduces its own client package and interface analogous to `aksmachinesheaderbatch`/`AKSMachinesHeaderBatchAPI`, if not introduce a new interface only in `azclient` package, with the implementation done by Azure SDK (more likely).

Beyond provisioning, changes in instance management as a result of API changes (if applicable) should be discussed case-by-case.

## FAQ

### Are we really okay with "shared timer across keys" and "not honoring original request context cancellation"?

AWS provider's implementation shares the same gaps and has been reasonably battle-tested. Our implementation will also be scale-tested to prove that.

## Appendix

### AWS comparison

* Instead of AKS machines API w/ or w/o batch header, AWS always uses the naturally-batched EC2 fleet API to create instances. AKS/Azure currently does not have a practical equivalent.
* AWS also implements a generic [`Batcher[T, U]`](https://github.com/aws/karpenter-provider-aws/blob/main/pkg/batcher/batcher.go), which inspired the implementation here. The relevant flow and abstractions from instance provider to the batcher are similar, with differences in how the API is called and how module boundaries are drawn.
  * E.g., [`CreateFleetBatcher`](https://github.com/aws/karpenter-provider-aws/blob/main/pkg/batcher/createfleet.go) has a similar role to the AKS machines header batch client, but is included in the batcher module rather than having a separate module like `aksmachinesheaderbatch`.
* AWS's implementation currently shares the same gaps indicated earlier.
  * Shared timer instead of per-batch timer
  * Does not cancel pending batch after original context cancellation
