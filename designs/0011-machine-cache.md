# AKS Machine Cache

**Status:** Implemented

## Overview

The AKS Machine Cache is a TTL-based caching layer that sits between Karpenter and the AKS Machine API. It reduces API call load by maintaining a cache of AKS machine instances.

## Architecture

### Cache Structure

The cache is implemented in `pkg/providers/instance/machinecache/` with the following key components:

- **Storage**: `map[string]*armcontainerservice.Machine` for storing machine instances by name
- **TTL Management**: Atomic timestamp tracking with configurable TTL (default 30 seconds)
- **Thread Safety**: `sync.RWMutex` for concurrent read/write access
- **Background Worker**: Goroutine that processes update requests asynchronously
- **Update Channel**: Buffered channel (size 1) for coalescing update requests

### Cache Lifecycle

1. **Initialization**: Cache created with `New()` at provider startup
2. **Background Worker**: Goroutine launched to handle cache updates
3. **Stale Detection**: Cache freshness checked via TTL comparison on each access
4. **Refresh on Stale**: Stale cache triggers background update via non-blocking channel
5. **API Fallback**: Stale or missing entries fall back to direct API calls

## Core Operations

### Get Operations

The cache provides `GetWithFallback(ctx, machineName, useCache)` which:

1. **Cache Hit (Fresh)**: Returns cached machine immediately
2. **Cache Miss or Stale**: Falls back to direct AKS Machine API GET
3. **Cache Update**: Stores fetched machine in cache for future use

**Cache Usage in Get**:

- **Drift Detection** (`pkg/cloudprovider/drift.go`): Uses cache (`WithCache()`)
  - Gets machine to check if cluster-level provisioning config has drifted server-side (reflected in DriftAction field)
  - Core drift controller calls IsDrifted on pod events, generating high API call volume
  - Current mitigation: Karpenter restarts on config changes, triggering IsDrifted at startup to catch drift
  - Cache is acceptable because: (1) reduces API load significantly, (2) drift detection is eventually consistent (caught on next pod event and IsDrifted calls in core have a 5m requeue), (3) Karpenter restarts ensure fresh data on config changes

- **Pre-Create Checks** (`pkg/providers/instance/aksmachineinstance.go`): Uses cache
  - Checks if machine already exists before creating (handles restart scenarios).
  - Safe to use cache because we fall back on a direct API call on cache misses. This check also just needs to verify the existance of a machine.

- **Post-Create Validation** (`pkg/providers/instance/aksmachineinstance.go`): No cache
  - Gets machine immediately after creation to retrieve VMResourceID and check for early provisioning errors
  - Direct API call ensures correctness and is unlikely to hit cache anyway since the machine was just created

- **In-Place Update** (`pkg/controllers/nodeclaim/inplaceupdate/controller.go`): No cache
  - Direct API call without cache option to ensure fresh data for update operations

- **Polling** (`pkg/providers/instance/machinecache/machinecache.go`): Uses cache
  - Polls cache for provisioning state changes during machine creation
  - Safe because the cache periodically refreshes after TTL expires (by default 30s).

- **CloudProvider Get** (`pkg/cloudprovider/cloudprovider.go`): No cache
  - Direct API call without cache option (always fresh data)
  - Karpenter Core could change the way cloudprovider Get is used so we prioritize correctness.

### List Operations

The cache provides `ListWithFallback(ctx, useCache)` which:

1. **Cache Hit (Fresh)**: Returns all cached machines
2. **Cache Stale**: Falls back to direct AKS Machine API LIST
3. **Filtering**: Automatically filters to Karpenter-managed machines (via nodepool tag)

Currently, all calls to List set useCache=false, but the option to use the cache exists for consistency with Get and in case we decide to use it in the future.

**Cache Usage in List**:

- **CloudProvider List** (`pkg/cloudprovider/cloudprovider.go`): No cache
  - List operations always fall directly to API calls currently, though the Option to use the cache does exist.
  - No need to use the cache because List calls are not overly frequent and we haven't found a need to optimize it with cache.

## Configuration

### Options

```go
// Default configuration
opts := opts{
    ttl:          30 * time.Second,  // Cache freshness duration
    pollInterval: 5 * time.Second,   // Polling check interval
    pollTimeout:  15 * time.Minute,  // Maximum polling duration
}

// Customization
WithTTL(duration)           // Override TTL
WithPollInterval(duration)  // Override poll interval
WithPollTimeout(duration)   // Override poll timeout
```

### Initialization

```go
cache := New(ctx, client, resourceGroup, clusterName, poolName, opts...)
```

The cache automatically spawns a background worker goroutine to handle refresh requests.

## Polling Integration

The cache provides a specialized `PollUntilDone(ctx, name)` method for waiting on machine provisioning:

1. **Initial Check**: Verify machine exists via `GetWithFallback`
2. **Ticker Loop**: Poll cache every `pollInterval` seconds
3. **State Check**: Call `pollOnce()` to check provisioning state
4. **Terminal States**: Return on Success, Failed, or Deleting
5. **Non-Terminal**: Continue polling on Creating/Updating states

This allows instance creation to wait for provisioning completion without repeated API calls.

## Benefits

- **Reduced GET Throttling**: Cache significantly reduces AKS Machine API GET calls for high-frequency operations like drift detection, polling, and pre-create checks
- **Correctness via Fallback**: Cache misses and stale cache entries automatically fall back to direct API calls, ensuring correctness without significant performance impact

