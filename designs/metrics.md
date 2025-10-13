# Metrics Design: Co-located Component-Specific Metrics

## Overview

This design document explains the approach for organizing metrics in the Karpenter Azure provider, specifically focusing on **component-specific metrics that are co-located with their implementation** rather than being centralized.

## First Example: Instance Provider Metrics

The instance provider serves as the **first example** of this co-location pattern, demonstrating how metrics should be organized across the codebase.

### 1. Created Component-Specific Metrics File

**File Created:** `pkg/providers/instance/metrics.go`

This file contains metrics specific to the instance provider component. As the first implementation of this pattern, it includes VM creation tracking metrics:

```go
package instance

var (
    VMCreateStartMetric             // Tracks operations starting
    VMCreateSyncFailureMetric       // Tracks synchronous failures
    VMCreateAsyncFailureMetric      // Tracks asynchronous failures
    VMCreateResponseErrorMetric     // Tracks response errors
)
```

### 2. Updated Component Implementation

**File Modified:** `pkg/providers/instance/instance.go`

The component uses its co-located metrics directly, without needing centralized metric imports:

**Before:**
```go
import "github.com/Azure/karpenter-provider-azure/pkg/metrics"

metrics.ComponentMetric.Inc(ctx, "description", metrics.Label(value))
```

**After:**
```go
// No separate metrics import needed - metrics are in same package

ComponentMetric.WithLabelValues(value).Inc()
```

## Design Principles

### Metric Namespace Identification

To distinguish Azure provider metrics from core Karpenter metrics, we use a **different namespace**:

- **Core Karpenter metrics**: `karpenter_*` (from upstream Karpenter)
- **Azure provider metrics**: `karpenter_azure_*` (Azure-specific implementations)

This is configured in `pkg/metrics/constants.go`:

```go
const (
    Namespace = "karpenter_aks"  // Identifies Azure provider metrics
)
```

**Why This Matters:**
- **Clear separation** between core and provider-specific metrics
- **Avoids conflicts** with upstream Karpenter metric names
- **Easy filtering** in monitoring systems (e.g., `karpenter_aks_*` for Azure-only metrics)
- **Consistent naming** across all Azure provider components

**Metric Naming Examples:**
- `karpenter_aks_instance_vm_create_start_total` - Azure provider metric
- `karpenter_nodeclaims_created_total` - Core Karpenter metric

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
        Namespace: coremetrics.Namespace,  // From centralized constants
        Subsystem: componentSubsystem,
        Name:      "operation_total",
        Help:      "Total number of operations.",
    },
    []string{"label1", "label2"},  // Component-specific labels
)
```

**Label Selection Guidelines:**
- Choose labels that help diagnose component-specific issues
- Keep cardinality reasonable (avoid high-cardinality labels like instance IDs)
- Use labels that align with how the component operates

### Label Constants

Defined locally in each component's metrics file:

```go
const (
    componentSubsystem = "component_name"
    label1 = "label_one"
    label2 = "label_two"
)
```

These are **component-specific** and don't need to be shared unless multiple components use identical labels.

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

## Centralized Metrics Package Role

The `pkg/metrics/` package remains important but serves a **different purpose**:

### Purpose: Infrastructure & Shared Utilities

1. **Constants**
   ```go
   const (
       Namespace = "karpenter_aks"  // Shared across all metrics
   )
   ```

2. **Builder Patterns**
   ```go
   func NewSubsystemMetrics(subsystem string, labels []string, buckets []float64) *SubsystemMetrics
   ```

## Migration Path for Other Components

When adding metrics to other components, follow this pattern:

### 1. Create Component Metrics File

```go
// pkg/providers/<component>/metrics.go
package component

import (
    coremetrics "github.com/Azure/karpenter-provider-azure/pkg/metrics"
    "github.com/prometheus/client_golang/prometheus"
    crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
    componentSubsystem = "component_name"
)

var (
    MyComponentMetric = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: coremetrics.Namespace,
            Subsystem: componentSubsystem,
            Name:      "operation_total",
            Help:      "Total number of operations.",
        },
        []string{"operation_type"},
    )
)

func init() {
    crmetrics.Registry.MustRegister(MyComponentMetric)
}
```

### 2. Use Metrics in Component Code

```go
// pkg/providers/<component>/component.go
package component

func (c *Component) DoOperation() {
    MyComponentMetric.WithLabelValues("create").Inc()
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
