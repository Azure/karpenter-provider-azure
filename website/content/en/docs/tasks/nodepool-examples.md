---
title: "NodePool Examples"
linkTitle: "NodePool Examples"
weight: 20
description: >
  Practical NodePool configurations for common scenarios
---

This guide provides practical NodePool configurations for common scenarios in both NAP and self-hosted deployments. Use these examples as starting points for your own configurations.

## Basic NodePool Configurations

### General Purpose NodePool

A versatile NodePool suitable for most workloads:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: general-purpose
  annotations:
    kubernetes.io/description: "General purpose NodePool for generic workloads"
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
  limits:
    cpu: 100
  disruption:
    consolidationPolicy: WhenUnderutilized
    expireAfter: Never
```

### Cost-Optimized with Spot Instances

Reduce costs using Azure Spot VMs with on-demand fallback:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: spot-optimized
  annotations:
    kubernetes.io/description: "Cost-optimized NodePool using spot instances"
spec:
  template:
    metadata:
      labels:
        cost-profile: spot
    spec:
      requirements:
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot", "on-demand"]  # Prefer spot, fallback to on-demand
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E", "F"]  # Multiple families for better availability
        - key: topology.kubernetes.io/zone
          operator: In
          values: ["westus2-1", "westus2-2", "westus2-3"]  # All zones
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
      # Add toleration for spot instances
      taints:
        - key: "spot-instance"
          value: "true"
          effect: NoSchedule
  limits:
    cpu: 200
  disruption:
    consolidationPolicy: WhenUnderutilized
    expireAfter: 2160h  # 90 days
  weight: 10  # Lower priority than on-demand pools
```

### High-Performance Computing

For CPU-intensive workloads requiring powerful VMs:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: compute-intensive
  annotations:
    kubernetes.io/description: "High-performance NodePool for CPU-intensive workloads"
spec:
  template:
    metadata:
      labels:
        workload-type: compute-intensive
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["F", "H"]  # Compute-optimized families
        - key: karpenter.azure.com/sku-cpu
          operator: Gt
          values: ["8"]  # Minimum 8 vCPUs
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: compute-nodeclass
      taints:
        - key: "compute-intensive"
          value: "true"
          effect: NoSchedule
  limits:
    cpu: 500
  disruption:
    consolidationPolicy: WhenEmpty  # Don't consolidate active compute nodes
    expireAfter: Never
  weight: 50  # High priority for matching workloads
```

## GPU NodePools

### NVIDIA GPU NodePool

For AI/ML workloads requiring NVIDIA GPUs:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: gpu-nvidia
  annotations:
    kubernetes.io/description: "GPU NodePool for AI/ML workloads"
spec:
  template:
    metadata:
      labels:
        workload-type: gpu
        gpu-type: nvidia
    spec:
      requirements:
        - key: karpenter.azure.com/sku-gpu-count
          operator: Gt
          values: ["0"]
        - key: karpenter.azure.com/sku-gpu-manufacturer
          operator: In
          values: ["nvidia"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["NC", "ND", "NV"]  # GPU families
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]  # GPUs typically on-demand only
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: gpu-nodeclass
      taints:
        - key: "gpu"
          value: "nvidia"
          effect: NoSchedule
      startupTaints:
        - key: "nvidia.com/gpu"
          value: "present"
          effect: NoSchedule
  limits:
    cpu: 100
  disruption:
    consolidationPolicy: WhenEmpty
    expireAfter: Never
  weight: 100  # Highest priority for GPU workloads
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: gpu-nodeclass
  annotations:
    kubernetes.io/description: "NodeClass for GPU instances with NVIDIA drivers"
spec:
  imageFamily: Ubuntu2204
  osDiskSizeGB: 256  # Larger disk for GPU workloads
```

### Multi-GPU Configuration

For workloads requiring multiple GPUs:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: multi-gpu
spec:
  template:
    metadata:
      labels:
        gpu-count: multi
    spec:
      requirements:
        - key: karpenter.azure.com/sku-gpu-count
          operator: Gt
          values: ["1"]  # More than 1 GPU
        - key: karpenter.azure.com/sku-gpu-manufacturer
          operator: In
          values: ["nvidia"]
        - key: karpenter.azure.com/sku-name
          operator: In
          values: ["Standard_NC24rs_v3", "Standard_ND40rs_v2"]  # Specific multi-GPU SKUs
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: gpu-nodeclass
      taints:
        - key: "gpu"
          value: "multi"
          effect: NoSchedule
  limits:
    cpu: 50
  weight: 90
```

## Memory-Optimized NodePools

### High-Memory Workloads

For memory-intensive applications:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: memory-optimized
  annotations:
    kubernetes.io/description: "Memory-optimized NodePool for data processing"
spec:
  template:
    metadata:
      labels:
        workload-type: memory-intensive
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["E", "M"]  # Memory-optimized families
        - key: karpenter.azure.com/sku-memory
          operator: Gt
          values: ["32768"]  # Minimum 32GB RAM
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand", "spot"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: memory-nodeclass
      taints:
        - key: "memory-intensive"
          value: "true"
          effect: NoSchedule
  limits:
    cpu: 200
  disruption:
    consolidationPolicy: WhenUnderutilized
    expireAfter: 720h  # 30 days
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: memory-nodeclass
spec:
  imageFamily: Ubuntu2204
  osDiskSizeGB: 128
```

## Multi-Architecture Support

### ARM64 and AMD64 NodePool

Support both architectures for diverse workloads:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: multi-arch
  annotations:
    kubernetes.io/description: "Multi-architecture NodePool supporting ARM64 and AMD64"
spec:
  template:
    metadata:
      labels:
        architecture: multi-arch
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64", "arm64"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "Dpds", "E", "Epds"]  # Both x64 and ARM families
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand", "spot"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
  limits:
    cpu: 150
  disruption:
    consolidationPolicy: WhenUnderutilized
  weight: 20
```

## Zone-Specific NodePools

### Single Zone NodePool

For applications that need to stay in one zone:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: zone-specific
spec:
  template:
    metadata:
      labels:
        zone: single
    spec:
      requirements:
        - key: topology.kubernetes.io/zone
          operator: In
          values: ["westus2-1"]  # Single zone
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
  limits:
    cpu: 50
```

## Storage-Optimized NodePools

### Premium SSD NodePool

For workloads requiring high-performance storage:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: storage-optimized
spec:
  template:
    metadata:
      labels:
        storage-type: premium
    spec:
      requirements:
        - key: karpenter.azure.com/sku-storage-premium-capable
          operator: In
          values: ["true"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: premium-storage-nodeclass
      taints:
        - key: "storage-intensive"
          value: "true"
          effect: NoSchedule
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: premium-storage-nodeclass
spec:
  imageFamily: Ubuntu2204
  osDiskSizeGB: 512  # Larger OS disk
  osDiskType: Premium_LRS
```

### Large Ephemeral Storage

For applications needing large temporary storage:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: large-ephemeral
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-storage-ephemeralos-maxsize
          operator: Gt
          values: ["200"]  # More than 200GB ephemeral storage
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["L"]  # Storage optimized
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: large-storage-nodeclass
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: large-storage-nodeclass
spec:
  imageFamily: Ubuntu2204
  osDiskSizeGB: 1024  # 1TB OS disk
```

## Security-Focused NodePools

### Isolated Workloads

For workloads requiring enhanced security:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: secure-isolated
spec:
  template:
    metadata:
      labels:
        security-level: high
      annotations:
        security.policy/isolation: "required"
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["DC", "DCS"]  # Confidential computing families when available
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]  # Only on-demand for security
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: secure-nodeclass
      taints:
        - key: "security-high"
          value: "true"
          effect: NoSchedule
      startupTaints:
        - key: "security-scanning"
          value: "pending"
          effect: NoSchedule
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: secure-nodeclass
spec:
  imageFamily: Ubuntu2204
  osDiskSizeGB: 128
```

## Development and Testing NodePools

### Development Environment

Cost-effective configuration for development:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: development
spec:
  template:
    metadata:
      labels:
        environment: dev
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["B"]  # Burstable instances for dev
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]  # Spot only for cost savings
        - key: karpenter.azure.com/sku-cpu
          operator: Lt
          values: ["8"]  # Limit CPU for cost control
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
      taints:
        - key: "environment"
          value: "dev"
          effect: NoSchedule
  limits:
    cpu: 20  # Lower limits for dev
  disruption:
    consolidationPolicy: WhenUnderutilized
    expireAfter: 24h  # Aggressive expiration for cost savings
  weight: 5  # Low priority
```

## NAP-Specific Configurations

### NAP with Node Image Pinning

Pin specific node image versions in NAP:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: nap-pinned-image
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: pinned-image-nodeclass
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: pinned-image-nodeclass
spec:
  imageFamily: Ubuntu2204
  imageVersion: "202311.07.0"  # Pin to specific image version
```

### NAP with Resource Limits

Control resource usage in NAP:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: nap-limited
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
  limits:
    cpu: "1000"     # Hard limit on total CPU
    memory: "1000Gi" # Hard limit on total memory
  disruption:
    consolidationPolicy: WhenUnderutilized
    consolidateAfter: 30s
```

## Best Practices

### NodePool Naming

Use descriptive names that indicate the NodePool's purpose:

```yaml
# Good examples
metadata:
  name: gpu-ml-training
  name: spot-batch-processing  
  name: memory-optimized-analytics
  name: arm64-web-services

# Avoid generic names
metadata:
  name: nodepool1
  name: test
  name: pool
```

### Labels and Annotations

Add meaningful labels and annotations:

```yaml
template:
  metadata:
    labels:
      team: platform
      cost-center: engineering
      workload-type: web-services
      capacity-type: spot
    annotations:
      docs.example.com/runbook: "https://docs.example.com/runbooks/web-services"
      contact.example.com/team: "platform-team@example.com"
```

### Taints for Workload Isolation

Use taints to ensure workloads run on appropriate nodes:

```yaml
spec:
  template:
    spec:
      taints:
        - key: "workload-type"
          value: "gpu"
          effect: NoSchedule
        - key: "cost-optimization"
          value: "spot"
          effect: NoSchedule
```

### Resource Limits

Set appropriate limits to control costs:

```yaml
limits:
  cpu: 100        # Total CPU cores across all nodes
  memory: 400Gi   # Total memory across all nodes
```

### Disruption Policies

Configure disruption based on workload characteristics:

```yaml
disruption:
  # For stateless workloads
  consolidationPolicy: WhenUnderutilized
  expireAfter: 24h

  # For stateful workloads  
  consolidationPolicy: WhenEmpty
  expireAfter: Never

  # For batch workloads
  consolidationPolicy: WhenUnderutilized
  expireAfter: 1h
```

## Troubleshooting NodePool Issues

### Common Problems

**Pods not scheduling**:
- Check NodePool requirements match pod constraints
- Verify NodePool limits haven't been exceeded
- Check for conflicting taints

**Unexpected costs**:
- Review NodePool limits
- Check spot vs on-demand configuration
- Verify disruption policies

**Performance issues**:
- Ensure appropriate VM families are selected
- Check resource requirements alignment
- Verify zone distribution

### Debug Commands

```bash
# Check NodePool status
kubectl get nodepools

# Describe NodePool for events
kubectl describe nodepool <nodepool-name>

# Check node labels and taints
kubectl get nodes --show-labels
kubectl describe node <node-name>

# Monitor Karpenter events
kubectl get events -A --field-selector source=karpenter -w
```

## Examples Repository

Find more examples in the [Karpenter Azure repository](https://github.com/Azure/karpenter-provider-azure/tree/main/examples/v1beta1), including:

- Complete application deployments
- Terraform configurations
- Monitoring setups
- Security configurations