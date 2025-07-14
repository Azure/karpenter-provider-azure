---
title: "Instance Types"
linkTitle: "Instance Types"
weight: 6
description: >
  Evaluate Instance Types
---

Karpenter provisions instances from Azure's VM sizes with the following support criteria:

**Supported VM sizes must have:**
- At least 2 vCPUs
- At least 3.5 GiB of memory
- Compatibility with AKS (Azure Kubernetes Service)
- Support for standard Azure managed disks

**Excluded VM sizes:**
- Basic tier VMs (Basic_A0 through Basic_A4)
- Very small Standard VMs (Standard_A0, Standard_A1, Standard_A1_v2, Standard_B1s, Standard_B1ms, Standard_F1, Standard_F1s)
- Confidential computing VMs (DC-series, EC-series) - *planned for future support*
- VMs with constrained vCPUs
- GPU VMs without proper image family support

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

Azure organizes VM sizes into families based on their intended use case and performance characteristics:

### General Purpose

**A-series**
- Entry-level economical VMs for development and testing
- Best for low-traffic web servers, proof of concepts
- Limited performance for production workloads

**B-series**  
- Burstable performance with baseline CPU performance
- Cost-effective for workloads with variable CPU usage
- Ideal for web servers, small databases, development environments
- CPU credits system allows bursting above baseline when needed

**D-series**
- Balanced CPU-to-memory ratio for enterprise applications  
- Suitable for web servers, application servers, small to medium databases
- Supports premium storage for better I/O performance
- Good for batch processing and relational databases

**DC-series**
- Confidential computing with hardware-based trusted execution environments
- Enhanced data protection for sensitive workloads
- Supports encryption of data in use
- Currently not supported by Karpenter

### Compute Optimized

**F-series**
- High CPU-to-memory ratio optimized for compute-intensive workloads
- Ideal for medium-traffic web servers, batch processing, analytics
- Good for gaming servers, scientific modeling
- Supports premium storage and accelerated networking

**FX-series**
- Specialized for compute-intensive workloads
- Excellent for Electronic Design Automation (EDA)
- Financial modeling and scientific simulations
- High-frequency processors with large L3 cache

### Memory Optimized

**E-series**
- High memory-to-CPU ratios for memory-intensive applications
- Ideal for relational databases, in-memory analytics
- Supports large-scale enterprise applications
- Good for big data processing and caching layers

**M-series**
- Extremely large memory configurations (up to 3.8 TiB)
- Designed for the largest enterprise databases
- SAP HANA and other large in-memory databases
- High-memory business intelligence applications

### Storage Optimized

**L-series**
- High disk throughput and I/O for storage-intensive workloads
- Local NVMe SSD storage with high IOPS
- Ideal for big data, NoSQL databases (Cassandra, MongoDB)
- Data warehousing and distributed file systems
- Video processing and rendering

### GPU Accelerated

**NC-series**
- NVIDIA GPU acceleration for compute-intensive workloads
- Machine learning training and inference
- High-performance computing (HPC) simulations
- 3D graphics rendering and video processing

**ND-series**  
- Optimized for deep learning training at scale
- Multiple high-end NVIDIA GPUs with NVLink
- Distributed AI training workloads
- Research and development in AI/ML

**NV-series**
- Graphics-intensive applications and virtual desktops
- GPU acceleration for visualization workloads
- 3D rendering, computer-aided design (CAD)
- Virtual desktop infrastructure (VDI)

### High Performance Computing (HPC)

**HB-series**
- High memory bandwidth for HPC applications
- AMD EPYC processors with large L3 cache
- Scientific simulations and modeling
- Computational fluid dynamics (CFD)

**HC-series**
- Intel Xeon processors optimized for HPC
- Dense compute workloads
- Weather forecasting and seismic analysis
- Engineering simulations

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
apiVersion: karpenter.sh/v1
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

## GPU Support and Image Compatibility

GPU-enabled VMs require specific image families for proper driver support:

**NVIDIA GPU VMs (NC, ND, NV series):**
- Supported with Ubuntu 22.04 image family
- Automatic NVIDIA driver installation
- CUDA runtime environment

**Azure Linux GPU VMs:**
- Supported with AzureLinux image family  
- Optimized for specific GPU workloads
- Limited to compatible GPU SKUs

```yaml
# Example GPU NodePool with proper image family
apiVersion: karpenter.sh/v1
kind: NodePool  
metadata:
  name: gpu-workloads
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-gpu-count
          operator: Gt
          values: ["0"]
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: gpu-nodeclass
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: gpu-nodeclass
spec:
  imageFamily: Ubuntu2204  # Required for NVIDIA GPU support
```

## Regional Availability

VM size availability varies by Azure region. Not all VM sizes are available in all regions. Karpenter automatically discovers which VM sizes are available in your cluster's region and zones.

**Zonal regions** (with availability zone support):
- Americas: East US, East US 2, Central US, South Central US, West US 2, West US 3, Canada Central, Brazil South
- Europe: North Europe, West Europe, France Central, Germany West Central, UK South, Sweden Central, Switzerland North
- Asia Pacific: Southeast Asia, East Asia, Australia East, Japan East, Korea Central, Central India

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
- Use burstable B-series for variable workloads understand the tradeoff of performance
- Consider Spot VMs for batch processing
- Right-size your instances based on actual usage

### Performance
- Use premium storage capable VMs for I/O intensive workloads
- Enable accelerated networking for network-intensive applications  
- Choose appropriate CPU-to-memory ratios for your workload type

### Ephemeral OS Disks

Karpenter uses a **failover logic** for OS disk provisioning where the `osDiskSizeGB` specification in your NodeClass is the primary constraint that determines disk type selection.

#### Disk Selection Logic

**1. Primary Constraint: osDiskSizeGB is Always Honored**
- The `osDiskSizeGB` value from your AKSNodeClass specification is always respected
- This size determines the actual OS disk that will be provisioned on the VM

**2. Ephemeral vs Managed Disk Decision**
- **Ephemeral Disk Used**: If `osDiskSizeGB` ≤ available ephemeral storage capacity on the VM
- **Managed Disk Used**: If `osDiskSizeGB` > available ephemeral storage capacity on the VM

**3. Automatic Placement Selection** (when ephemeral disk is used)
Karpenter automatically chooses the best ephemeral disk placement:

**Placement Priority (best to worst):**
1. **NVMe Disk**: Highest performance local NVMe SSD
2. **Cache Disk**: High-performance caching disk 
3. **Resource Disk**: Standard temporary disk

#### Customer Configuration Guidelines

**To Guarantee Ephemeral Disk Usage:**
Set your `osDiskSizeGB` to be smaller than or equal to the ephemeral storage capacity of your target VM sizes. This ensures Karpenter will always use ephemeral disks instead of falling back to managed disks.

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: ephemeral-optimized
spec:
  osDiskSizeGB: 128  # Fits within most VM ephemeral capacities
```

**Common Ephemeral Capacities by VM Family:**
- **Standard_D4s_v3**: ~150GB cache disk available
- **Standard_E4s_v3**: ~100GB cache disk available  
- **Standard_F4s_v2**: ~30GB resource disk available
- **Standard_L8s_v3**: ~1.4TB NVMe disk available

**Example: Guaranteed Ephemeral Usage**
```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: guaranteed-ephemeral
spec:
  template:
    spec:
      requirements:
        # Target D-series VMs with sufficient ephemeral capacity
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D"]
        - key: karpenter.azure.com/sku-storage-ephemeral-os-max-size
          operator: Gt
          values: ["128"]  # Ensure VMs can fit our 128GB requirement
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: ephemeral-128gb
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: ephemeral-128gb
spec:
  osDiskSizeGB: 128  # Will use ephemeral disk on selected VMs
```

**Example: Large Disk (Likely Managed)**
```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: large-disk
spec:
  osDiskSizeGB: 500  # Exceeds most ephemeral capacities, will use managed disk
```

#### Benefits of Ephemeral OS Disks
- **Faster boot times**: No network-attached storage latency
- **Better I/O performance**: Local storage for OS operations
- **No additional storage costs**: Included with VM pricing
- **Automatic optimization**: Karpenter selects the best available ephemeral placement

#### Key Considerations
- **Size Trade-off**: Smaller `osDiskSizeGB` values increase the likelihood of ephemeral disk usage
- **VM Selection**: Use the `karpenter.azure.com/sku-storage-ephemeral-os-max-size` label to target VMs with sufficient ephemeral capacity
- **Workload Requirements**: Ensure your applications can work with the available disk space when optimizing for ephemeral disks


## Spot VMs
Azure Spot VMs offer significant cost savings (up to 90% off) in exchange for potential interruption when Azure needs the capacity back.

```yaml
apiVersion: karpenter.sh/v1
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
- **Preemption**: May be reclaimed when Azure needs capacity
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


Configure nodepools with spot and on-demand to fall back to on-demand capacity when you run out of spot capacity! 
