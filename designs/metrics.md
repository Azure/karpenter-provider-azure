# Metrics Design: Co-located Component-Specific Metrics

## Overview

This design document explains the approach for organizing metrics in the Karpenter Azure provider, specifically focusing on **component-specific metrics that are co-located with their implementation** rather than being centralized.

## First Example: Instance Provider Metrics

The instance provider serves as the **first example** of this co-location pattern, demonstrating how metrics should be organized across the codebase.

### 1. Created Component-Specific Metrics File

**File Created:** `pkg/providers/instance/metrics.go`

This file contains metrics specific to the instance provider component. As the first implementation of this pattern, it currently ships two VM creation counters:

```go
package instance

var (
    VMCreateStartMetric   // Counts create attempts initiated
    VMCreateFailureMetric // Counts create attempts that failed
)
```

* `VMCreateStartMetric` increments every time we attempt to create a VM.
* `VMCreateFailureMetric` increments for any failure and uses the `phase` label (`sync` or `async`) plus an `error_code` label extracted from Azure ARM responses so callers can distinguish failure modes.

### 2. Updated Component Implementation

**File Modified:** `pkg/providers/instance/vminstance.go`

The component uses its co-located metrics directly, without needing centralized metric imports:

**Before:**
```go
import "github.com/Azure/karpenter-provider-azure/pkg/metrics"

metrics.ComponentMetric.Inc(ctx, "description", metrics.Label(value))
```

**After:**
```go
// No separate metrics import needed - metrics are in same package

VMCreateStartMetric.With(map[string]string{
    metrics.ImageLabel: imageID,
}).Inc()

VMCreateFailureMetric.With(map[string]string{
    metrics.ImageLabel:     imageID,
    metrics.PhaseLabel:     "sync",
    metrics.ErrorCodeLabel: "OperationNotAllowed",
}).Inc()
```

## Design Principles

### Metric Namespace Identification

To distinguish Azure provider metrics from core Karpenter metrics, we evaluated using a provider-specific namespace but ultimately decided to **keep emitting metrics in the core `karpenter_*` namespace** for consistency.

This is configured in `pkg/metrics/constants.go`:

```go
const (
    Namespace = "karpenter"  // Reuse core namespace for consistency
)
```

**Why This Matters:**
- **Clear expectations** for consumers already relying on the core namespace
- **Avoids conflicts** with upstream Karpenter metric names
- **Keeps dashboards/alerts working** without additional filtering
- **Consistent naming** across all components as we upstream more features

**Metric Naming Examples:**
- `karpenter_instance_vm_create_start_total` - Azure provider metric (new)
- `karpenter_nodeclaims_created_total` - Core Karpenter metric

### Namespace Rationale

We are intentionally keeping provider metrics in the core `karpenter_*` namespace so that:

- **Compatibility with upstream tooling** remains intact—existing dashboards and alerts maintained by the Karpenter community continue to function. For example, our controller still emits core metrics by wrapping the provider implementation with `metrics.Decorate` in `cmd/controller/main.go` (see [`cmd/controller/main.go#L63`](https://github.com/Azure/karpenter-provider-azure/blob/main/cmd/controller/main.go#L63)).
- **Stability expectations** stay aligned—metrics such as `ProvisioningDurationSeconds` in `pkg/cloudprovider/cloudprovider.go` (see [`cloudprovider.go#L224`](https://github.com/Azure/karpenter-provider-azure/blob/main/pkg/cloudprovider/cloudprovider.go#L224)) already ship in this namespace and have consumers.
- **Clear subsystem identification** still distinguishes our metrics—Azure-specific metrics use distinct subsystem names (e.g., `karpenter_instance_*`, `karpenter_pricing_*`) that make it clear which metrics originate from our provider versus core Karpenter components.

In practice, the controller will continue to emit the decorated core metrics for compatibility, and new Azure-specific metrics (such as the VM create counters) will also use the `karpenter` namespace to maintain a single, consistent surface area. This keeps expectations aligned with upstream Karpenter and simplifies future maintenance and documentation.

### Co-location Pattern

Following Kubernetes best practices, **production Prometheus metrics are co-located with their implementation components**:

1. **Provider-specific metrics** → Live within provider packages (e.g., `pkg/providers/instance/`)
2. **Controller-specific metrics** → Live within controller packages (e.g., `pkg/controllers/*/`)
3. **Component-specific metrics** → Live with the component (batcher, cache, etc.)

### Why Co-location?

#### 1. **Locality of Reference**
- Metrics are defined next to the code that uses them
- Easy to understand what metrics a component exposes
- Reduces cognitive load when reading/maintaining code

#### 2. **Package Encapsulation**
- Each component owns its metrics
- Changes to component behavior and metrics happen together
- No cross-package dependencies for metrics

## Specific Changes in Detail

### Label Design

Each component defines **labels relevant to its operations**:

```go
// Example from instance provider
ComponentMetric = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: labels.Namespace,  // From centralized constants
        Subsystem: componentSubsystem,
        Name:      "operation_total",
        Help:      "Total number of operations.",
    },
    []string{labels.value1, labels.value2},  // Component-specific labels shared from pkg/metrics/constants.go
)
```

**Label Selection Guidelines:**
- Choose labels that help diagnose component-specific issues
- Keep cardinality reasonable (avoid high-cardinality labels like instance IDs)
- Use labels that align with how the component operates

### Label Constants

Defined locally in each component's metrics file (snake_case is expected for metric related labels) when they are specific to that component. The component subsystem name stays local, while shared label keys should live in `pkg/metrics/constants.go` to prevent duplication:

```go
const (
    componentSubsystem = "component_name"
)

// Shared labels imported from pkg/metrics/constants.go via
// metrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
var (
    capacityTypeLabel = metrics.LabelCapacityType
    zoneLabel         = metrics.LabelZone
)
```

Component-only constants stay in the metrics file, while reusable labels are centralized so multiple components can rely on the same canonical names without redefining them.

### Safe Metric Recording

To prevent label ordering issues, use the `.With()` method that accepts maps instead of positional arguments:

```go
// SAFE: Order-independent, self-documenting
ComponentMetric.With(map[string]string{
    coremetrics.LabelCapacityType: capacityType,
    coremetrics.LabelZone:         zone,
    coremetrics.ImageLabel:        imageID,
}).Inc()

// UNSAFE: Positional arguments can be accidentally swapped
// ComponentMetric.WithLabelValues(capacityType, zone, imageID).Inc() // What if you accidentally swap zone and imageID?
```

### Metric Call Pattern

Using standard Prometheus API:

```go
// Simple, direct metric recording
ComponentMetric.WithLabelValues(value1, value2).Inc()
```

**Benefits:**
- Standard Prometheus pattern familiar to all Go developers
- No custom abstraction to learn
- Direct control over label values
- Clear at call site what's being measured


### Purpose: Infrastructure & Shared Utilities

1. **Constants**
   ```go
   const (
       Namespace = "karpenter"  // Shared across all metrics
       LabelCapacityType = "capacity_type"
       LabelZone         = "zone"
       LabelErrorCode    = "error"
   )
   ```

   Shared label keys live here so multiple components can reuse the same canonical names without redefining them locally.

## Migration Path for Other Components

When adding metrics to other components, follow this pattern:

### 1. Create Component Metrics File

```go
// pkg/providers/<component>/metrics.go
package component

import (
    labels "github.com/Azure/karpenter-provider-azure/pkg/metrics"
    "github.com/prometheus/client_golang/prometheus"
    metrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
    componentSubsystem = "component_name"
)

var (
    MyComponentMetric = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: labels.Namespace,
            Subsystem: componentSubsystem,
            Name:      "operation_total",
            Help:      "Total number of operations.",
        },
        []string{labels.LabelCapacityType},
    )
)

func init() {
    metrics.Registry.MustRegister(MyComponentMetric)
}
```

### 2. Use Metrics in Component Code

```go
// pkg/providers/<component>/component.go
package component

func (c *Component) DoOperation() {
    MyComponentMetric.With(map[string]string{
        coremetrics.LabelCapacityType: "spot",
    }).Inc()
    // ... operation logic
}
```

## Benefits of This Approach

1. **Scalability**: Easy to add new metrics per component without central bottleneck
2. **Clarity**: One file to check for all metrics a component exposes
3. **Maintainability**: Component changes include metric changes in same PR
4. **Testability**: Can mock/test component metrics independently
5. **Documentation**: Metrics documented where they're defined
6. **Standard Practice**: Follows Kubernetes and Prometheus best practices

## Metrics Organization Pattern

```
pkg/
├── metrics/                          # Shared infrastructure
│   ├── metrics.go                    # Truly shared metrics (if any)
│   ├── constants.go                  # Namespace constants
│   ├── builder.go                    # Helper builders
│   ├── context.go                    # MetricContext helper
│   └── buckets.go                    # Bucket presets
├── providers/
│   ├── instance/
│   │   ├── instance.go               # Implementation
│   │   └── metrics.go                # Instance metrics ✓ First example
│   ├── pricing/
│   │   ├── pricing.go
│   │   └── metrics.go                # Pricing metrics (future)
│   └── launchtemplate/
│       ├── launchtemplate.go
│       └── metrics.go                # Template metrics (future)
└── controllers/
    └── nodeclaim/
        ├── controller.go
        └── metrics.go                # Controller metrics (future)
```

## Conclusion

The instance provider serves as the **first implementation** of this pattern, demonstrating how metrics should be organized going forward.

This approach provides:
- **Clear separation** between infrastructure (centralized) and implementation (co-located)
- **Better organization** following Kubernetes patterns
- **Easier maintenance** with metrics near their usage
- **Flexibility** for components to evolve independently
- **Standard patterns** that any Go/Prometheus developer recognizes

The centralized `pkg/metrics/` package provides **shared utilities and constants**, while **component-specific production metrics live with their implementations**, making the codebase more maintainable and following established cloud-native patterns.

Future components should follow the instance provider example when adding their own metrics.

## Future Metric Types

Currently, the instance provider implementation uses **Counters** to track events (VM creation starts, failures, errors). As the metrics infrastructure matures, additional Prometheus metric types will be implemented to provide richer observability:

### Histograms

Histograms will measure **operation durations and distributions**, enabling:

- Tracking of latency percentiles (p50, p95, p99)
- Performance degradation identification
- SLO-based alerting (e.g., "95% of operations under X seconds")

**Use cases:**
- VM creation latency
- API call duration
- Image selection time
- Resource provisioning times

### Gauges

Gauges will measure **current state and resource levels**, providing:

- Real-time resource utilization monitoring
- Queue depth and backpressure tracking
- Cache efficiency observation
- Capacity constraint detection

**Use cases:**
- Active provisioning operations
- Cached entries count
- Available capacity by zone
- Throttled requests count
