# Logging Guidelines for Azure Karpenter Provider

## Log Levels and When to Use Them

### Error Level (`Error`)

**When to use Error:** Failures that prevent normal operation
- External system failures (Azure API, network)
- Authentication/authorization failures
- Resource cleanup failures that may cause leaks
- Unrecoverable errors that affect operation
- Any operation that fails to complete its intended goal (even if retryable)

**Examples:**
```go
log.FromContext(ctx).Error(err, "failed to authenticate with Azure")
log.FromContext(ctx).Error(err, "VM creation failed, cleaning up resources")
log.FromContext(ctx).Error(err, "failed to cleanup VM, may cause resource leak")
```

**Example Output:**
```json
{"level":"ERROR","time":"2024-01-01T12:00:00Z","msg":"failed to authenticate with Azure","error":"authentication failed: invalid credentials"}
{"level":"ERROR","time":"2024-01-01T12:00:05Z","msg":"VM creation failed, cleaning up resources","error":"quota exceeded for region"}
{"level":"ERROR","time":"2024-01-01T12:00:10Z","msg":"failed to cleanup VM, may cause resource leak","error":"timeout waiting for deletion"}
```

### Warning Level (`V(-1)`)

**Note: Generally avoid `V(-1)` unless truly warranted.** Use `Error` for operation failures and `V(-1)` for operations that succeed or continue with unexpected behavior.

**When to use V(-1):** For non-fatal issues that operators should be aware of
- Recoverable errors that don't prevent operation but indicate potential issues
- Invariant violations that suggest system inconsistencies
- Fallback scenarios where default behavior is used
- Skipped operations due to precondition failures
- Resource state inconsistencies that are handled gracefully

**Examples:**
```go
log.FromContext(ctx).V(-1).Info("duplicate node error detected, invariant violated")
log.FromContext(ctx).V(-1).Info("kubernetes version not ready, skipping drift check", "error", err)
log.FromContext(ctx).V(-1).Info("failed to get zone for VM, zone label will be empty", "vmName", vmName, "error", err)
log.FromContext(ctx).V(-1).Info("detected potential kubernetes downgrade, keeping current version", "currentKubernetesVersion", currentK8sVersion, "discoveredKubernetesVersion", newK8sVersion)
```

**Example Output:**
```json
{"level":"WARN","time":"2024-01-01T12:00:00Z","msg":"duplicate node error detected, invariant violated"}
{"level":"WARN","time":"2024-01-01T12:00:05Z","msg":"kubernetes version not ready, skipping drift check","error":"version API unavailable"}
{"level":"WARN","time":"2024-01-01T12:00:10Z","msg":"failed to get zone for VM, zone label will be empty","vmName":"aks-nodepool-12345-vm-000000","error":"zone metadata not found"}
{"level":"WARN","time":"2024-01-01T12:00:15Z","msg":"detected potential kubernetes downgrade, keeping current version","currentKubernetesVersion":"1.28.5","discoveredKubernetesVersion":"1.28.0"}
```

### Info Level (`V(0)`)

**When to use Info:** For important events that operators care about, these are logged by default
- Successful completion of major operations
- Important state changes (node creation/deletion)
- System startup/shutdown events
- Configuration discoveries that affect behavior

**Examples:**
```go
log.FromContext(ctx).WithValues("vmName", vmName).Info("successfully created VM")
log.FromContext(ctx).WithValues("nodeCount", count).Info("scaled cluster")
log.FromContext(ctx).WithValues("version", version).Info("karpenter starting")
```

**Example Output:**
```json
{"level":"INFO","time":"2024-01-01T12:00:00Z","msg":"successfully created VM","vmName":"aks-nodepool-12345-vm-000000"}
{"level":"INFO","time":"2024-01-01T12:00:05Z","msg":"scaled cluster","nodeCount":3}
{"level":"INFO","time":"2024-01-01T12:00:10Z","msg":"karpenter starting","version":"v0.37.0"}
```

### Debug Level (`V(1)`)

**When to use V(1):** For operational details useful for troubleshooting
- Progress indicators for long-running operations
- Detailed success confirmations
- Resource lifecycle events (NIC creation, extension installation)
- Retry attempts and recovery actions
- Performance metrics (duration, counts)

**Examples:**
```go
log.FromContext(ctx).WithValues("duration", duration).V(1).Info("VM creation completed")
log.FromContext(ctx).WithValues("skuCount", len(skus)).V(1).Info("discovered instance types")
log.FromContext(ctx).WithValues("nicName", nicName).V(1).Info("created network interface")
log.FromContext(ctx).WithValues("attempt", retryCount).V(1).Info("retrying operation")
```

**Example Output:**
```json
{"level":"DEBUG","time":"2024-01-01T12:00:00Z","msg":"VM creation completed","duration":"45.2s"}
{"level":"DEBUG","time":"2024-01-01T12:00:05Z","msg":"discovered instance types","skuCount":24}
{"level":"DEBUG","time":"2024-01-01T12:00:10Z","msg":"created network interface","nicName":"aks-nodepool-12345-nic-000000"}
{"level":"DEBUG","time":"2024-01-01T12:00:15Z","msg":"retrying operation","attempt":2}
```

### Trace Level (`V(2)`+)

**When to use V(2)+:** For detailed debugging information
- Per-request details and API calls
- Internal state transitions
- Verbose debugging information
- Full request/response payloads (V(3))

**Examples:**
```go
// V(2) Examples:
log.FromContext(ctx).WithValues("params", params).V(2).Info("calling Azure API")
log.FromContext(ctx).WithValues("vmName", vmName).V(2).Info("processing VM request")
log.FromContext(ctx).WithValues("offering", offering).V(2).Info("evaluating instance offering")

// V(3) Examples:
log.FromContext(ctx).WithValues("response", response).V(3).Info("received API response")
log.FromContext(ctx).WithValues("state", internalState).V(3).Info("internal state dump")
```

**Example Output:**
```json
// V(2) - Per-request debugging
{"level":"DEBUG","time":"2024-01-01T12:00:00Z","msg":"calling Azure API","params":{"resourceGroupName":"rg-test","vmName":"test-vm"}}
{"level":"DEBUG","time":"2024-01-01T12:00:05Z","msg":"processing VM request","vmName":"aks-nodepool-12345-vm-000000"}
{"level":"DEBUG","time":"2024-01-01T12:00:10Z","msg":"evaluating instance offering","offering":"Standard_D4s_v5"}

// V(3) - Verbose debugging
{"level":"DEBUG","time":"2024-01-01T12:00:15Z","msg":"received API response","response":{"id":"/subscriptions/.../vm-000000","status":"Creating"}}
{"level":"DEBUG","time":"2024-01-01T12:00:20Z","msg":"internal state dump","state":{"pendingVMs":3,"availableCapacity":"80%"}}
```

## WithValues vs Direct Key-Value Pairs

**Use `WithValues()` for context that applies to multiple log statements:**
- Fields like resource names, IDs, or regions that provide context for all logs in a scope
- When creating a logger instance that will be reused multiple times

**Use direct key-value pairs for single-use data:**
- Values only relevant for a specific log message (durations, counts, attempt numbers)
- Ephemeral data that doesn't need to appear in every log

**Examples:**
```go
// WithValues: Context for multiple operations
logger := log.FromContext(ctx).WithValues("vmName", vmName, "resourceGroupName", rg)
logger.Info("creating VM")
logger.Info("VM created successfully")

// Direct pairs: Single-use data
log.FromContext(ctx).Info("processing instances", "instanceCount", len(instances))
log.FromContext(ctx).Error(err, "failed to create VM", "attempt", retryCount)
```

**Example Output:**
```json
// WithValues - vmName and resourceGroupName appear in both logs automatically
{"level":"INFO","time":"2024-01-01T12:00:00Z","msg":"creating VM","vmName":"aks-nodepool-12345-vm-000000","resourceGroupName":"rg-test"}
{"level":"INFO","time":"2024-01-01T12:00:05Z","msg":"VM created successfully","vmName":"aks-nodepool-12345-vm-000000","resourceGroupName":"rg-test"}

// Direct pairs - single-use data appears only in specific logs
{"level":"INFO","time":"2024-01-01T12:00:10Z","msg":"processing instances","instanceCount":5}
{"level":"ERROR","time":"2024-01-01T12:00:15Z","msg":"failed to create VM","attempt":3,"error":"timeout exceeded"}
```

## Key Naming Conventions

When adding new logging keys, follow these conventions:

### Key Naming
- **Descriptive**: Keys should clearly indicate what they represent
- **Consistent**: Use the same key name across different log statements for the same variables
- **Camel Case Default**: Use camel case for most logging keys
  -  `driftType`, `vmName`, `resourceGroupName`
- **Azure Resources**: Use `Name` suffix for resources that have both name and ID properties
  - Name: `vmName`, `nicName`, `extensionName`
  - ID: `vmID`, `nicID`, `extensionID`
- **Kubernetes Resource**: Match Kubernetes syntax for well-known Kubernetes resources, labels, and fields
  - Kebab Case for labels: `instance-type`, `capacity-type`
  - Capitalized Camel Case for resources: `Node`, `NodeClaim`, `NodePool`, `Pod`
  - Camel Case for fields: `providerID`

### Common Key Examples
- **Azure Resources (camelCase)**: `vmName`, `nicName`, `resourceGroupName`, `loadBalancerName`, `extensionName`
- **Azure Resource IDs (camelCase)**: `vmID`, `nicID`, `extensionID`
- **Kubernetes Resources (capitalized CamelCase)**: `Node`, `NodeClaim`, `NodePool`
- **Kubernetes Labels/Annotations (kebab-case)**: `instance-type`, `capacity-type`
- **Kubernetes Fields (camelCase)**: `providerID`
- **Versioning (camelCase)**: `currentKubernetesVersion`, `discoveredKubernetesVersion`, `expectedKubernetesVersion`, `actualKubernetesVersion`
- **Drift Detection (camelCase)**: `driftType`, `nodeClassHash`, `nodeClaimHash`, `actualImageID`
- **Operations (camelCase)**: `goalHash`, `actualHash`
- **Counts (camelCase)**: `instanceTypeCount`, `skuCount`, `loadBalancerCount`
- **Identity (camelCase)**: `expectedKubeletIdentityClientID`, `actualKubeletIdentityClientID`



## Summary

| Level   | Purpose                                         | Examples                                                     | Zapr Output Level |
| ------- | ----------------------------------------------- | ------------------------------------------------------------ | ----------------- |
| `Error` | Failures that prevent operation                 | API failures, cleanup failures                               | ERROR             |
| `V(-1)` | Warnings for non-fatal issues *(use sparingly)* | Invariant violations, fallback scenarios, skipped operations | WARN              |
| `Info`  | Important operational events                    | VM created, cluster scaled, service started                  | INFO              |
| `V(1)`  | Operational details for troubleshooting         | Progress updates, detailed successes, retries                | DEBUG             |
| `V(2)`  | Per-request debugging                           | API calls, request processing                                | DEBUG             |
| `V(3)`  | Verbose debugging                               | Full payloads, internal state                                | DEBUG             |

*Note: In Zapr, logr `V(-1)` maps to WARN output, `V(0)` maps to INFO, and `V(1)` and higher map to DEBUG. The V(-1) level provides a way to log warnings that operators should see without requiring debug logging to be enabled. See [Zapr README: Increasing Verbosity](https://github.com/go-logr/zapr?tab=readme-ov-file#increasing-verbosity) for details.*
