# GET-Based Poller for AKS Machine Provisioning

**Last updated:** Feb 9, 2026

**Status:** Completed

## Overview

A custom GET-based poller that tracks individual AKS Machine provisioning status by
polling `GET /machines/{name}` at a configurable interval until the machine reaches a
terminal state (Succeeded or Failed).

### Goals

* Provide per-machine provisioning status tracking when the Azure SDK's built-in LRO
  poller is unavailable (e.g., batch creation returns one poller for N machines).
* Support configurable poll intervals, retry delays, and maximum retries.
* Be compatible with both batch and non-batch create paths.

One concrete use case is batch creation (design 0011): the batch coordinator sends one
API call for N machines but only receives one SDK poller for the whole batch. Each
machine needs its own status tracking, which the GET-based poller provides.

### Non-Goals

* Replace the SDK poller for non-batch cases — when available, the SDK poller is preferred
  since it handles Azure-specific retry/redirect semantics automatically.

## Design

The poller lives in `pkg/providers/instance/aksmachinepoller`.

### Configuration

```go
type Options struct {
    PollInterval      time.Duration // Time between GET calls (default: 5s)
    InitialRetryDelay time.Duration // Initial retry delay on transient errors (default: 1s)
    MaxRetryDelay     time.Duration // Maximum retry delay with exponential backoff (default: 30s)
    MaxRetries        int           // Maximum transient error retries before giving up (default: 10)
}
```

`AKSMachineProvider` stores a `pollerOptions` field with production defaults.
`SetPollerOptions` allows overriding for testing (e.g., 1ms intervals for fast completion).

### Usage in the promise wait path

The AKS Machine promise's wait function selects the poller based on whether an SDK poller
was returned from the create call:

```go
if poller == nil {
    // Batch case: SDK poller not available, use GET-based poller
    getPoller := aksmachinepoller.NewPoller(p.pollerOptions, ...)
    provisioningErr, pollerErr := getPoller.PollUntilDone(ctx)
    // ...
}
```

## Decisions

### Why a separate poller instead of reusing the SDK's?

The SDK poller is per-request — it tracks one `BeginCreateOrUpdate` call. In batch mode,
the coordinator sends a single API call for N machines. Only one SDK poller is returned for
the entire batch, which cannot track individual machines. Each machine needs its own poller,
and a GET-based approach is the most straightforward.

### Why configurable options?

Production needs conservative intervals (5s poll, exponential backoff on errors) to avoid
overloading the API. Tests need near-instant completion (1ms intervals) to avoid unnecessary
delays. Making the options injectable via `SetPollerOptions` keeps both cases clean without
test-specific code paths.

## Testing

`InstantPollerOptions()` provides a test-friendly configuration with 1ms poll intervals:

```go
func InstantPollerOptions() aksmachinepoller.Options {
    return aksmachinepoller.Options{
        PollInterval:      1 * time.Millisecond,
        InitialRetryDelay: 1 * time.Millisecond,
        MaxRetryDelay:     1 * time.Millisecond,
        MaxRetries:        3,
    }
}
```

The GET poller still executes in test, but completes almost instantly.
