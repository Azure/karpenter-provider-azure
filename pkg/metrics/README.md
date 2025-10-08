# Karpenter Azure Metrics Infrastructure

This package provides a reusable, extensible metrics infrastructure for the Karpenter Azure provider. The design follows patterns from [cloud-provider-azure](https://github.com/kubernetes-sigs/cloud-provider-azure) to ensure consistency and ease of use.

## Overview

The metrics infrastructure provides:

- **Standardized metric types** (duration histograms, error counters, success counters)
- **Consistent naming conventions** across subsystems
- **Pre-tuned histogram buckets** for different operation types
- **MetricContext helper** for easy metric recording

## Architecture

### Core Components

```
pkg/metrics/
├── metrics.go       # Main registry and metric definitions
├── constants.go     # Namespace and subsystem constants
├── builder.go       # SubsystemMetrics builder pattern
├── context.go       # MetricContext for operation tracking
└── buckets.go       # Pre-tuned histogram buckets
```

### Current Metrics

The following metrics are currently implemented:

- `karpenter_image_selection_error_count` - Counter for image selection errors

## Adding New Metrics

### Option 1: Quick Single Metric

For adding a single metric without creating a full subsystem:

```go
// In pkg/metrics/metrics.go
var (
    MyNewMetric = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: Namespace,
            Subsystem: "my_subsystem",
            Name:      "operation_total",
            Help:      "Total number of operations.",
        },
        []string{"operation", "result_code"}, // Define your labels
    )
)

func init() {
    crmetrics.Registry.MustRegister(
        ImageSelectionErrorCount,
        MyNewMetric, // Add here
    )
}
```

### Option 2: Full Subsystem with Standard Metrics

For adding a complete subsystem with duration, error, and success metrics:

#### Step 1: Define the Subsystem Constant

```go
// In pkg/metrics/constants.go
const (
    Namespace = "karpenter"

    imageFamilySubsystem = "image"
    instanceSubsystem    = "instance"  // Add your subsystem
)
```

#### Step 2: Create Subsystem Metrics

```go
// In pkg/metrics/instance.go (new file)
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
    // InstanceMetrics provides standard metrics for instance operations
    InstanceMetrics = NewSubsystemMetrics(
        instanceSubsystem,
        []string{"operation", "instance_type", "zone", "result_code"}, // Define your labels
        VMOperationBuckets, // Use pre-tuned buckets
    )
)

func init() {
    // Register all instance metrics
    crmetrics.Registry.MustRegister(InstanceMetrics.Collectors()...)
}
```

#### Step 3: Use in Your Code

```go
// In your controller/provider code
import "github.com/Azure/karpenter-provider-azure/pkg/metrics"

func (p *Provider) CreateInstance(ctx context.Context) error {
    // Create metric context
    mc := metrics.NewMetricContext(
        "instance",
        prometheus.Labels{
            "operation":     "create",
            "instance_type": "Standard_D2s_v3",
            "zone":          "eastus-1",
            "result_code":   "", // Will be set in defer
        },
    )

    // Record metrics on completion
    defer func() {
        if err != nil {
            mc.labels["result_code"] = "error"
            mc.ObserveError(metrics.InstanceMetrics.ErrorCounter)
        } else {
            mc.labels["result_code"] = "success"
            mc.ObserveSuccess(metrics.InstanceMetrics.SuccessCounter)
        }
        mc.ObserveDuration(metrics.InstanceMetrics.DurationHistogram)
    }()

    // Your implementation...
    return nil
}
```

## Label Guidelines

### Consistent Label Naming

Use consistent label names across all metrics in your subsystem:

```go
// ✅ Good - consistent naming
labels := prometheus.Labels{
    "operation":   "create",
    "result_code": "success",
}

// ❌ Bad - inconsistent naming
labels := prometheus.Labels{
    "op":     "create",
    "status": "success",
}
```

## Histogram Buckets

Choose appropriate buckets based on operation type:

| Bucket Set | Use Case | Range |
|------------|----------|-------|
| `APICallBuckets` | Azure ARM API calls, metadata lookups | 100ms - 5min |
| `VMOperationBuckets` | VM create/delete/scale operations | 1s - 10min |
| `QuickOperationBuckets` | Cache lookups, local operations | 10ms - 5s |
| `DefaultBuckets` | General purpose operations | 5ms - 10s |

```go
// Example: Choose buckets based on operation speed
fastMetrics := metrics.NewSubsystemMetrics(
    "cache",
    []string{"operation"},
    metrics.QuickOperationBuckets,
)

slowMetrics := metrics.NewSubsystemMetrics(
    "provisioning",
    []string{"operation"},
    metrics.VMOperationBuckets,
)
```

## Testing

When writing tests for code that records metrics:

```go
import (
    "testing"
    "github.com/prometheus/client_golang/prometheus"
    dto "github.com/prometheus/client_model/go"
)

func TestMetricsRecorded(t *testing.T) {
    // Your test code that triggers metrics...

    // Verify metric was recorded
    metrics := collectMetrics(t, InstanceMetrics.SuccessCounter)
    if len(metrics) == 0 {
        t.Fatal("Expected success metric to be recorded")
    }
}

func collectMetrics(t *testing.T, collector prometheus.Collector) []*dto.Metric {
    t.Helper()
    ch := make(chan prometheus.Metric, 100)
    collector.Collect(ch)
    close(ch)

    var metrics []*dto.Metric
    for metric := range ch {
        m := &dto.Metric{}
        if err := metric.Write(m); err != nil {
            t.Fatal(err)
        }
        metrics = append(metrics, m)
    }
    return metrics
}
```

## References

- [Prometheus Best Practices](https://prometheus.io/docs/practices/naming/)
- [Prometheus Metric Types](https://prometheus.io/docs/concepts/metric_types/)
- [cloud-provider-azure metrics](https://github.com/kubernetes-sigs/cloud-provider-azure/blob/master/pkg/metrics/)
- [Karpenter Core Metrics](https://karpenter.sh/docs/reference/metrics/)
