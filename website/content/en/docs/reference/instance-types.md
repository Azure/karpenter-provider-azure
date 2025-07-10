---
title: "Instance Types"
linkTitle: "Instance Types"
weight: 6
description: >
  Evaluate Instance Types
---

Karpenter provisions instances from Azure's VM sizes. Karpenter supports all VM sizes except those with fewer than 2 vCPUs, specialized VM sizes that don't support standard Azure managed disks, or VM sizes that are not available in AKS.

## Viewing Available Instance Types

You can view instance types available for your cluster by running:

```bash
kubectl get aksnodeclass default -o jsonpath='{.status.instances[*]}'
```

The complete list of instance types is discoverable programmatically. Karpenter surfaces this information through the `AKSNodeClass` status for instances supported in your region and zones.

```bash
kubectl describe aksnodeclass default
```

## VM Size Families

Azure organizes VM sizes into families based on their intended use case:

### General Purpose
- **D-series**: Balanced CPU-to-memory ratio. Good for testing, development, small databases, and low traffic web servers.
- **E-series**: Optimized for in-memory applications. High memory-to-CPU ratio.
- **B-series**: Burstable performance. Cost-effective for workloads that don't need full CPU performance continuously.

### Compute Optimized  
- **F-series**: High CPU-to-memory ratio. Good for compute-intensive applications.

### Memory Optimized
- **M-series**: Highest memory-to-CPU ratios. Ideal for large databases and in-memory analytics.

### Storage Optimized
- **L-series**: High disk throughput and IO. Good for big data, NoSQL databases.

### GPU Accelerated
- **N-series**: GPU-enabled VMs for AI, machine learning, and high-performance computing.

## Instance Type Selection

### Karpenter Labels

When Karpenter provisions instances, it attaches several Azure-specific labels:

```yaml
# VM SKU information
karpenter.azure.com/sku-name: "Standard_D4s_v3"
karpenter.azure.com/sku-family: "D"
karpenter.azure.com/sku-version: "3"

# Hardware specifications
karpenter.azure.com/sku-cpu: "4"
karpenter.azure.com/sku-memory: "16384"

# Capabilities
karpenter.azure.com/sku-networking-accelerated: "true"
karpenter.azure.com/sku-storage-premium-capable: "true"

# GPU information (if applicable)
karpenter.azure.com/sku-gpu-name: "V100"
karpenter.azure.com/sku-gpu-manufacturer: "nvidia"  
karpenter.azure.com/sku-gpu-count: "1"
```

### Scheduling Constraints

You can use these labels in your NodePool requirements to target specific instance types:

```yaml
apiVersion: karpenter.sh/v1beta1
kind: NodePool
metadata:
  name: compute-optimized
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["F"]
        - key: karpenter.azure.com/sku-cpu
          operator: Gt
          values: ["8"]
```

## Common VM Sizes

### Small Workloads
- **Standard_B2s**: 2 vCPUs, 4 GB RAM - Burstable, cost-effective
- **Standard_D2s_v3**: 2 vCPUs, 8 GB RAM - General purpose

### Medium Workloads  
- **Standard_D4s_v3**: 4 vCPUs, 16 GB RAM - General purpose
- **Standard_E4s_v3**: 4 vCPUs, 32 GB RAM - Memory optimized

### Large Workloads
- **Standard_D8s_v3**: 8 vCPUs, 32 GB RAM - General purpose
- **Standard_E8s_v3**: 8 vCPUs, 64 GB RAM - Memory optimized
- **Standard_F8s_v2**: 8 vCPUs, 16 GB RAM - Compute optimized

### GPU Workloads
- **Standard_NC6s_v3**: 6 vCPUs, 112 GB RAM, 1x V100 GPU
- **Standard_NC12s_v3**: 12 vCPUs, 224 GB RAM, 2x V100 GPU
- **Standard_ND40rs_v2**: 40 vCPUs, 672 GB RAM, 8x V100 GPU

## Regional Availability

VM size availability varies by Azure region. Not all VM sizes are available in all regions. Karpenter automatically discovers which VM sizes are available in your cluster's region and zones.

You can check VM size availability in a region using Azure CLI:

```bash
az vm list-sizes --location eastus --output table
```

## Pricing Considerations

Azure VM pricing varies by:
- **VM size**: Larger VMs generally cost more
- **Region**: Some regions have different pricing
- **Pricing model**: On-demand vs. Spot VMs
- **Disk type**: Premium SSD costs more than Standard HDD

Karpenter automatically considers pricing when making provisioning decisions and will choose the most cost-effective VM size that meets your requirements.

## Best Practices

### NodePool Configuration
- Use a variety of instance types to increase availability
- Don't over-constrain requirements unless necessary
- Consider using Spot VMs for fault-tolerant workloads

### Cost Optimization
- Use burstable B-series for variable workloads
- Consider Spot VMs for batch processing
- Right-size your instances based on actual usage

### Performance
- Use premium storage capable VMs for I/O intensive workloads
- Enable accelerated networking for network-intensive applications  
- Choose appropriate CPU-to-memory ratios for your workload type

## Spot VMs

Azure Spot VMs offer significant cost savings (up to 90% off) in exchange for potential interruption when Azure needs the capacity back.

```yaml
apiVersion: karpenter.sh/v1beta1
kind: NodePool
metadata:
  name: spot-nodepool
spec:
  template:
    spec:
      requirements:
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]
```

### Spot VM Considerations
- **Interruption notice**: 30 seconds advance warning
- **No SLA**: No availability guarantee
- **Best effort**: Allocation depends on available capacity
- **Pricing**: Variable based on demand

Use Spot VMs for:
- Batch processing jobs
- Development/testing environments  
- Fault-tolerant applications
- Workloads with flexible timing

Avoid Spot VMs for:
- Production databases
- Real-time applications
- Stateful workloads without backup strategies
- Critical system components