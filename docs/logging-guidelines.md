# Logging Guidelines for Azure Karpenter Provider

## Log Levels and When to Use Them

### Error Level (`Error`)
**For failures that prevent normal operation**

```go
// Examples:
log.FromContext(ctx).Error(err, "failed to authenticate with Azure")
log.FromContext(ctx).Error(err, "VM creation failed, cleaning up resources")
log.FromContext(ctx).Error(err, "failed to cleanup VM, may cause resource leak")
```

**When to use Error:**
- External system failures (Azure API, network)
- Authentication/authorization failures
- Resource cleanup failures that may cause leaks
- Unrecoverable errors that affect operation

### Info Level (`V(0)`)
**For important events that operators care about, these are logged by default**

```go
// Examples:
log.FromContext(ctx).WithValues("vmName", vmName).Info("successfully created VM")
log.FromContext(ctx).WithValues("nodeCount", count).Info("scaled cluster")
log.FromContext(ctx).WithValues("version", version).Info("karpenter starting")
log.FromContext(ctx).WithValues("subscription", masked).Info("Azure client initialized")
```

**When to use Info:**
- Successful completion of major operations
- Important state changes (node creation/deletion)
- System startup/shutdown events
- Configuration discoveries that affect behavior

### Debug Level (`V(1)`)
**For operational details useful for troubleshooting**

```go
// Examples:
log.FromContext(ctx).WithValues("duration", duration).V(1).Info("VM creation completed")
log.FromContext(ctx).WithValues("skuCount", len(skus)).V(1).Info("discovered instance types")
log.FromContext(ctx).WithValues("nicName", nicName).V(1).Info("created network interface")
log.FromContext(ctx).WithValues("attempt", retryCount).V(1).Info("retrying operation")
```

**When to use V(1):**
- Progress indicators for long-running operations
- Detailed success confirmations
- Resource lifecycle events (NIC creation, extension installation)
- Retry attempts and recovery actions
- Performance metrics (duration, counts)

### Trace Level (`V(2)`+)
**For detailed debugging information**

```go
// V(2) Examples:
log.FromContext(ctx).WithValues("params", params).V(2).Info("calling Azure API")
log.FromContext(ctx).WithValues("vmName", vmName).V(2).Info("processing VM request")
log.FromContext(ctx).WithValues("offering", offering).V(2).Info("evaluating instance offering")

// V(3) Examples:
log.FromContext(ctx).WithValues("response", response).V(3).Info("received API response")
log.FromContext(ctx).WithValues("state", internalState).V(3).Info("internal state dump")
```

**When to use V(2)+:**
- Per-request details and API calls
- Internal state transitions
- Verbose debugging information
- Full request/response payloads (V(3))

## When to Log or Return Errors

### Controllers - Log Errors You Handle
```go
// Log because you're handling the error
if err := promise.Wait(); err != nil {
    log.FromContext(ctx).WithValues("vmName", vmName).Error(err, "VM failure")

    // Handle by cleaning up
    c.instanceProvider.Delete(ctx, vmName)
    return err
}

// Log async errors (when you don't return them)
go func() {
    if err := backgroundTask(); err != nil {
        log.FromContext(ctx).Error(err, "failure")
    }
}()
```

### Provider - Return Errors, Don't Log Them
```go
// Return error with context, don't log
func (p *Provider) CreateVM(ctx context.Context, name string) error {
    err := p.client.Create(ctx, params)
    if err != nil {
        return fmt.Errorf("failed to create VM %s: %w", name, err)
    }

    // Success logging is fine
    log.FromContext(ctx).WithValues("vmName", name).V(1).Info("created VM")
    return nil
}
```

### Exceptions: Cleanup and Best-Effort Operations
```go
// Log cleanup failures (best effort)
defer func() {
    if err := cleanup(); err != nil {
        log.FromContext(ctx).V(1).Error(err, "cleanup failed (non-fatal)")
    }
}()
```

## Summary

| Level | Purpose | Examples |
|-------|---------|----------|
| `Error` | Failures that prevent operation | API failures, cleanup failures |
| `Info` | Important operational events | VM created, cluster scaled, service started |
| `V(1)` | Operational details for troubleshooting | Progress updates, detailed successes, retries |
| `V(2)` | Per-request debugging | API calls, request processing |
| `V(3)` | Verbose debugging | Full payloads, internal state |
