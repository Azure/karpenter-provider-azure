# Batch Creation for AKS Machines

**Last updated:** Feb 9, 2026

**Status:** Completed

## Overview

Creating N machines via N individual API calls is slow and subject to per-request rate
limiting. Batch creation groups multiple machine create requests into a single API call,
reducing latency and API load.

### Goals

* Batch multiple AKS Machine creates with compatible templates into one API call.
* Transparent to callers — the batching client implements the same `AKSMachinesAPI`
  interface as the non-batching client.
* Configurable grouping parameters (idle timeout, max timeout, max batch size).

### Non-Goals

* Batching deletes or other operations — only creates are batched.

## Design

Three components form the batch pipeline:

```
Create requests → Grouper → Coordinator → Single API call
                  (groups by     (executes batch,
                   template)      fans out results)
```

### BatchingMachinesClient

`pkg/providers/instance/batch/client.go` — drop-in replacement implementing
`azclient.AKSMachinesAPI`. On `BeginCreateOrUpdate`, eligible requests are enqueued
into the Grouper; the caller blocks on a response channel.

```go
type BatchingMachinesClient struct {
    realClient    azclient.AKSMachinesAPI
    grouper       *Grouper
    resourceGroup, clusterName, poolName string
}
var _ azclient.AKSMachinesAPI = (*BatchingMachinesClient)(nil)
```

### Grouper

`pkg/providers/instance/batch/grouper.go` — collects requests, groups them by template
hash (machines with the same SKU/image/config), and dispatches to the Coordinator when
any of these conditions are met:

- **Idle timeout**: no new request arrived for N ms.
- **Max timeout**: the batch has been open for N ms.
- **Max batch size**: the batch reached the configured limit.

### Coordinator

`pkg/providers/instance/batch/coordinator.go` — executes a batch as a single API call
via the `BatchPutMachine` HTTP header, then distributes per-machine results back to each
request's response channel.

The SDK poller returned for the batch API call is discarded — it tracks the batch, not
individual machines. Each request's response has `Poller: nil`, which signals the
promise's wait function to use the GET-based poller (see design 0010) for per-machine
status tracking.

## Dependencies

This design depends on:

- **Instance promise WaitGroup**: With batching, `Create()` returns even earlier
  (the request is just enqueued into the Grouper), and the background goroutine runs
  longer (waiting for batch dispatch + GET polling). Deterministic synchronization is
  essential for test correctness.
- **GET-based poller (0010)**: Individual machines need their own polling mechanism since
  the SDK poller is per-batch, not per-machine.
