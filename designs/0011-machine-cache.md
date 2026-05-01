# AKS Machine Cache

**Status:** Implemented

## Overview

The AKS Machine Cache is a TTL-based caching layer that sits between Karpenter and the AKS Machine API. It reduces API call load by maintaining a local cache of AKS machine instances with automatic background refresh and fallback mechanisms.

## Architecture

### Cache Structure

The cache is implemented in `pkg/providers/instance/machinecache/` with the following key components:

- **Storage**: Thread-safe `sync.Map` for storing machine instances by name
- **TTL Management**: Atomic timestamp tracking with configurable TTL (default 30 seconds)
- **Background Worker**: Goroutine that processes update requests asynchronously
- **Update Channel**: Buffered channel (size 1) for coalescing update requests

### Cache Lifecycle

1. **Initialization**: Cache created with `NewMachineCache()` at provider startup
2. **Background Worker**: Goroutine launched to handle cache updates
3. **Stale Detection**: Cache freshness checked via TTL comparison on each access
4. **Automatic Refresh**: Stale cache triggers background update via non-blocking channel
5. **API Fallback**: Stale or missing entries fall back to direct API calls

## Core Operations

### Get Operations

The cache provides `GetWithFallback(ctx, machineName, useCache)` which:

1. **Cache Hit (Fresh)**: Returns cached machine immediately
2. **Cache Miss or Stale**: Falls back to direct AKS Machine API GET
3. **Cache Update**: Stores fetched machine in cache for future use

**Cache Usage in Get**:

- **Drift Detection** (`pkg/cloudprovider/drift.go`): Uses cache (`WithUseCache()`)
  - Drift checks occur frequently and benefit from cached data
  - Line 264: `aksMachine, err := c.aksMachineInstanceProvider.Get(ctx, aksMachineName, instance.WithUseCache())`

- **Pre-Create Checks** (`pkg/providers/instance/aksmachineinstance.go`): Uses cache
  - Line 440: `existingAKSMachine, err := p.machineCache.GetWithFallback(ctx, aksMachineName, true)`
  - Checks if machine already exists before creating (handles restart scenarios)

- **Post-Create Validation** (`pkg/providers/instance/aksmachineinstance.go`): Uses cache
  - Line 498: `gotAKSMachine, err := p.getCreatedMachineAndHandleEarlyProvisioningError(...)`
  - Line 729: `gotAKSMachine, err := p.machineCache.GetWithFallback(ctx, aksMachineName, true)`
  - Fetches machine properties immediately after creation

- **Polling** (`pkg/providers/instance/machinecache/machinecache.go`): Uses cache
  - Line 246: `machine, err := c.GetWithFallback(ctx, name, true)`
  - Polls cache for provisioning state changes during machine creation
  - Line 254: `aksMachine, found, fresh := c.getFromCache(aksMachineName)`

- **CloudProvider Get** (`pkg/cloudprovider/cloudprovider.go`): No cache
  - Line 425: `aksMachine, err := c.aksMachineInstanceProvider.Get(ctx, aksMachineName)`
  - Direct API call without cache option (always fresh data)

### List Operations

The cache provides `ListWithFallback(ctx, useCache)` which:

1. **Cache Hit (Fresh)**: Returns all cached machines
2. **Cache Stale**: Falls back to direct AKS Machine API LIST
3. **Filtering**: Automatically filters to Karpenter-managed machines (via nodepool tag)
4. **Order**: Results are unordered (due to `sync.Map.Range()` non-determinism)

**Cache Usage in List**:

- **CloudProvider List** (`pkg/cloudprovider/cloudprovider.go`): Uses cache
  - Line 456 (AKS machines): `aksMachineInstances, err := c.aksMachineInstanceProvider.List(ctx, instance.WithUseCache())`
  - List operations are expensive; cache significantly reduces API load

- **Currently**: List always uses cache when available (no opt-out path in production)
- **Design**: The `useCache` parameter provides flexibility to bypass cache if needed

## Cache Update Mechanism

### Update Flow

1. **Trigger**: Stale cache detected during Get/List operation
2. **Request**: Non-blocking send to `updateRequests` channel
3. **Coalescing**: Buffered channel (size 1) prevents duplicate updates
4. **Background**: Worker goroutine processes update request
5. **Refresh**: Fetch latest machines from API and update cache
6. **Pruning**: Remove machines no longer returned by API
7. **Timestamp**: Update last-modified timestamp on completion

### Update Implementation

The `update()` method (lines 296-337):
- Fetches all machines via paged API calls
- Validates machines (filters by Karpenter nodepool tag)
- Updates `sync.Map` with new/updated machines
- Prunes machines no longer in API response
- Updates freshness timestamp

## Configuration

### Options

```go
// Default configuration
opts := opts{
    ttl:          30 * time.Second,  // Cache freshness duration
    pollInterval: 5 * time.Second,   // Polling check interval
    disabled:     false,             // Cache enabled
}

// Customization
WithTTL(duration)           // Override TTL
WithPollInterval(duration)  // Override poll interval
WithCacheDisabled()         // Disable cache entirely
```

### Cache Disabled Mode

When disabled:
- No background worker spawned
- All Get/List operations fall back to API
- Zero memory overhead
- Used in testing scenarios

## Polling Integration

The cache provides a specialized `PollUntilDone(ctx, name)` method for waiting on machine provisioning:

1. **Initial Check**: Verify machine exists via `GetWithFallback`
2. **Ticker Loop**: Poll cache every `pollInterval` seconds
3. **State Check**: Call `pollOnce()` to check provisioning state
4. **Terminal States**: Return on Success, Failed, or Deleting
5. **Non-Terminal**: Continue polling on Creating/Updating states

This allows instance creation to wait for provisioning completion without repeated API calls.

## Benefits

### API Load Reduction

- **Drift Checks**: High-frequency drift detection uses cached data
- **List Operations**: Expensive list calls served from cache (TTL: 30s)
- **Polling**: Provisioning state checks use cache instead of repeated GETs

### Performance

- **Sub-millisecond Cache Hits**: In-memory `sync.Map` access
- **Background Updates**: Non-blocking refresh doesn't delay operations
- **Request Coalescing**: Multiple stale detections trigger single update

### Reliability

- **Fallback**: Stale/missing data falls back to API automatically
- **Consistency**: Background updates ensure cache freshness
- **Thread-Safe**: `sync.Map` and atomic operations prevent races

## Testing

The cache includes comprehensive test coverage in `machinecache_test.go`:

- `TestUpdate`: Cache update scenarios (fresh cache, nil pager, errors, pruning)
- `TestGetWithFallback`: Cache hit, cache miss, stale cache, API errors
- `TestListWithFallback`: List from cache, fallback to API, error handling
- `TestIsFresh`: TTL-based freshness detection
- `TestPollUntilDone`: Polling for provisioning completion
- `TestPollOnce`: Single poll iteration logic

## Future Enhancements

- **Metrics**: Cache hit/miss rates, API call reduction
- **Adaptive TTL**: Adjust TTL based on cache churn rate
- **Selective Invalidation**: Invalidate specific machines on known updates
- **Cache Warming**: Pre-populate cache on provider initialization
