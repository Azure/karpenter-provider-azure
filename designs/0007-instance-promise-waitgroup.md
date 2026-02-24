# Instance Promise WaitGroup: Deterministic Test Synchronization

**Last updated:** Feb 9, 2026

**Status:** Completed

## Overview

`CloudProvider.Create()` spawns a background goroutine that polls the instance promise,
waits for the `Launched` condition, and performs error handling/cleanup. Tests that assert
on API state after `Create()` race against this goroutine.

Currently there is no mechanism for tests to wait until these goroutines complete.
Without synchronization, any test that checks API call counts, error events, or
NodeClaim state after `Create()` is non-deterministic — it may pass or fail depending
on whether the goroutine happened to finish before the assertion ran. This becomes
worse as more async work is added to the Create path (e.g., additional API calls from
a GET-based poller), since the goroutine takes longer and the race window widens.

This design adds `sync.WaitGroup`-based tracking for deterministic synchronization.

### Goals

* Eliminate races between test assertions and in-flight async goroutines. Without
  synchronization, tests that assert on API call counts or NodeClaim state after
  `Create()` are non-deterministic and will flake.
* Full goroutine lifecycle tracking — `WaitForInstancePromises()` means all goroutines
  are truly done, including error handling, NodeClaim deletion, and metrics emission.
* Minimal production code footprint — only a `sync.WaitGroup` field and one accessor
  method added to `CloudProvider`.
* Enable reliable testing for future work that adds async behavior to the Create path.
  For example, the GET-based poller (design 0008) introduces background GET calls that
  mutate API call counters. Without the WaitGroup, tests that assert on GET call counts
  after `Create()` would non-deterministically fail depending on whether the poller
  goroutine completed before the assertion ran.

### Non-Goals

* Changing the async provisioning architecture — goroutines continue to run as before.
* Synchronizing anything other than the `handleInstancePromise` goroutines.

## Design

### WaitGroup on CloudProvider

```go
type CloudProvider struct {
    // ...
    instancePromiseWg sync.WaitGroup
}

func (c *CloudProvider) WaitForInstancePromises() {
    c.instancePromiseWg.Wait()
}
```

### Goroutine lifecycle

```go
c.instancePromiseWg.Add(1)            // Synchronous, before go
go func() {
    defer c.instancePromiseWg.Done()   // Full completion
    err := instancePromise.Wait()      // Poll until provisioned
    c.waitUntilLaunched(ctx, nodeClaim) // Wait for Launched condition
    // ... error handling, NodeClaim deletion, metrics
}()
```

### Test helpers

Two helpers in `pkg/test/expectations` wrap the common patterns:

| Helper | What it does |
|---|---|
| `ExpectProvisionedAndDrained` | Wraps upstream `ExpectProvisioned` (which sets `Launched=True` internally) + `WaitForInstancePromises()` |
| `CreateAndDrain` | Wraps `cloudProvider.Create()` + sets `Launched=True` + `WaitForInstancePromises()` |

Additionally, `AfterEach` blocks call `cloudProvider.WaitForInstancePromises()` directly
as a safety net before `Reset()`:

```go
AfterEach(func() {
    cloudProvider.WaitForInstancePromises()
    cluster.Reset()
    azureEnv.Reset()
})
```

### Synchronization flow

```
Test code                          Background goroutine
─────────                          ────────────────────
Create() / ExpectProvisioned()
  └─ instancePromiseWg.Add(1)
  └─ go func() {
                                     instancePromise.Wait()
                                     waitUntilLaunched()
Set Launched=True (if direct Create)
                                     // ... error handling, cleanup
                                     instancePromiseWg.Done()
                                   }
WaitForInstancePromises()          // blocks until Done()
Assert on API state
```

## Decisions

### `Add(1)` before `go`, not inside the goroutine

`Add(1)` is called synchronously in `handleInstancePromise`, before `go func()`. This
guarantees the counter is incremented before `Create()` returns to the caller, so any
subsequent `WaitForInstancePromises()` call always observes the pending goroutine.

If `Add(1)` were inside the goroutine, there would be a race: the caller might call
`WaitForInstancePromises()` before the goroutine schedules and increments the counter,
causing Wait to return immediately (missing the goroutine entirely).

### Full goroutine lifecycle, not partial

An earlier iteration signaled `Done()` after `instancePromise.Wait()` completes (before
`waitUntilLaunched`), giving "promise resolved" semantics. This was rejected:

- **Half-completion is surprising**: `WaitForInstancePromises` would not actually mean
  all goroutines are done — cleanup code (error handling, NodeClaim deletion, metrics)
  would still be running.
- **Tests need cleanup to complete**: Tests assert on deletion counts and error events
  that happen in the post-poller code path.

Instead, the WaitGroup tracks the full goroutine lifecycle, and tests unblock
`waitUntilLaunched` by setting `Launched=True`.

### Setting Launched=True in tests to unblock `waitUntilLaunched`

`waitUntilLaunched` polls the API server every 500ms until the NodeClaim's `Launched`
condition is non-Unknown (True or False). In production, the core lifecycle controller
sets this. In tests:

- **`ExpectProvisioned` (upstream)** already sets `Launched=True` via
  `ExpectNodeClaimDeployedNoNode` — no extra work needed.
- **Direct `Create()` calls** (via `CreateAndDrain`) did NOT set it, causing
  `waitUntilLaunched` to block forever → test deadlock with 4-minute timeout.

The fix: `CreateAndDrain` sets `Launched=True` after `Create()`, mimicking what the
lifecycle controller does in production.

### Status-only update to avoid "spec is immutable"

`CreateAndDrain` uses `Status().Update()` (status-only subresource update) instead of
`ExpectApplied` (which does a full object update including spec). This is necessary
because some tests create a NodeClaim with a modified spec (e.g., conflicted NodeClaim
tests with different zone/SKU), and updating the full object triggers the webhook's
"spec is immutable" validation:

```go
fresh := &karpv1.NodeClaim{}
azureEnv.Client().Get(ctx, types.NamespacedName{Name: nodeClaim.Name}, fresh)
fresh.StatusConditions().SetTrue(karpv1.ConditionTypeLaunched)
azureEnv.Client().Status().Update(ctx, fresh)  // status-only, no spec change
```

Note the fresh `Get` — we fetch the object as it exists in the API server rather than
reusing the test's modified copy.

### Import cycle and the `instancePromiseWaiter` interface

The helpers live in `pkg/test/expectations` because they're shared across 4+ test packages
(`cloudprovider`, `providers/instancetype`, `providers/instance`,
`controllers/nodeclaim/garbagecollection`).

`pkg/cloudprovider` test files import `pkg/test/expectations`, so `pkg/test/expectations`
cannot import `pkg/cloudprovider` back — that would be a Go import cycle. An unexported
interface in `expectations.go` breaks the cycle:

```go
type instancePromiseWaiter interface {
    WaitForInstancePromises()
}
```

Callers type-assert `corecloudprovider.CloudProvider` → `instancePromiseWaiter` at runtime.
`*cloudprovider.CloudProvider` satisfies it.

Alternatives considered:
- **Concrete `*cloudprovider.CloudProvider` parameter** — causes the import cycle.
- **Methods on `test.Environment`** — unnecessary indirection; Environment doesn't own
  the WaitGroup.
- **Separate sub-package** — a whole package for two functions is overkill.
- **Inline at call sites** — duplicates the Launched status update logic at 20+ call sites.

## Testing

### Fake poller behavior

In tests, `fake.MockHandler[T].Done()` always returns `true`, so the SDK's `PollUntilDone`
returns immediately with no delay. The async goroutine's wall-clock time is dominated by
`waitUntilLaunched`, which resolves as soon as the test sets `Launched=True`.
