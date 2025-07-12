---
title: "NodeClasses"
linkTitle: "NodeClasses"
weight: 2
description: >
  Configure Azure-specific settings with AKSNodeClasses
---

Node Classes enable configuration of Azure specific settings.
Each NodePool must reference an AKSNodeClass using `spec.template.spec.nodeClassRef`.
Multiple NodePools may point to the same AKSNodeClass.

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  template:
    spec:
      nodeClassRef:
        apiVersion: karpenter.azure.com/v1beta1
        kind: AKSNodeClass
        name: default
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: default
spec:
  # Optional, configures the VM image family to use
  imageFamily: Ubuntu2204

  # Optional, configures the VNET subnet to use for node network interfaces
  # If not specified, uses the default VNET subnet from Karpenter configuration
  vnetSubnetID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet"

  # Optional, configures the OS disk size in GB
  osDiskSizeGB: 128

  # Optional, configures kubelet settings
  kubelet:
    cpuManagerPolicy: "none"
    cpuCFSQuota: true
    cpuCFSQuotaPeriod: "100ms"
    imageGCHighThresholdPercent: 85
    imageGCLowThresholdPercent: 80
    topologyManagerPolicy: "none"
    allowedUnsafeSysctls: []
    containerLogMaxSize: "50Mi"
    containerLogMaxFiles: 5
    podPidsLimit: -1

  # Optional, configures the maximum number of pods per node
  maxPods: 30

  # Optional, propagates tags to underlying Azure resources
  tags:
    team: team-a
    app: team-a-app

status:
  # Resolved image information
  images:
    - id: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/images/ubuntu-2204-amd64"
      name: "ubuntu-2204-amd64"
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values:
            - amd64
    - id: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Compute/images/ubuntu-2204-arm64"
      name: "ubuntu-2204-arm64"
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values:
            - arm64
```

Refer to the [NodePool docs]({{<ref "./nodepools" >}}) for settings applicable to all providers. To explore various `AKSNodeClass` configurations, refer to the examples provided [in the Karpenter GitHub repository](https://github.com/Azure/karpenter-provider-azure/tree/main/examples/).

## spec.imageFamily

ImageFamily is an optional field that dictates the default VM image and bootstrapping logic for nodes provisioned through this `AKSNodeClass`. Currently, Karpenter supports `imageFamily` values `Ubuntu2204` and `AzureLinux`. If not specified, the default is `Ubuntu2204`. GPUs are supported with both image families on compatible VM sizes.

### Ubuntu2204

Ubuntu 22.04 is the default image family for AKS nodes. It provides a well-tested, stable foundation for Kubernetes workloads with full GPU support.

```bash
#!/bin/bash
# AKS Bootstrap Script for Ubuntu 22.04
/opt/azure/containers/provision_source.sh
```

### AzureLinux

Azure Linux is Microsoft's optimized Linux distribution designed specifically for Azure workloads. It provides improved performance and security for containerized applications.

```bash
#!/bin/bash
# AKS Bootstrap Script for Azure Linux
/opt/azure/containers/provision_source.sh
```

## spec.vnetSubnetID

The `vnetSubnetID` field specifies which Azure Virtual Network subnet should be used for provisioning node network interfaces. This field is optional; if not specified, Karpenter will use the default subnet configured in the Karpenter installation.

The subnet ID must be in the full Azure Resource Manager format:
```
/subscriptions/{subscription-id}/resourceGroups/{resource-group}/providers/Microsoft.Network/virtualNetworks/{vnet-name}/subnets/{subnet-name}
```

{{% alert title="Note" color="primary" %}}
The specified subnet must be in the same region as your AKS cluster and must have sufficient IP addresses available for node provisioning. The subnet should also be configured with appropriate Network Security Group rules to allow cluster communication.
{{% /alert %}}

### Default Subnet Behavior

The `vnetSubnetID` field in AKSNodeClass is optional. When not specified, Karpenter automatically uses the default subnet configured during Karpenter installation via the `--vnet-subnet-id` CLI parameter or `VNET_SUBNET_ID` environment variable.

This default subnet is typically the same subnet specified during AKS cluster creation with the `--vnet-subnet-id` flag in the `az aks create` command. This creates a fallback mechanism where:

- **With vnetSubnetID specified**: Karpenter provisions nodes in the specified custom subnet
- **Without vnetSubnetID specified**: Karpenter provisions nodes in the cluster's default subnet (from `--vnet-subnet-id`)

This allows you to have a mix of NodeClasses - some using custom subnets for specific workloads, and others using the cluster's default subnet configuration.

## spec.osDiskSizeGB

The `osDiskSizeGB` field specifies the size of the OS disk in gigabytes. The default value is 128 GB, and the minimum value is 30 GB. This setting controls the root disk size for the VM instances.

```yaml
spec:
  osDiskSizeGB: 256  # 256 GB OS disk
```

{{% alert title="Note" color="primary" %}}
Larger OS disk sizes may be necessary for workloads that store significant data locally or require additional space for container images. Consider the cost implications of larger disk sizes.
{{% /alert %}}

## spec.maxPods

The `maxPods` field specifies the maximum number of pods that can be scheduled on a node. This is an important setting that affects both cluster density and network configuration.

The default behavior depends on the network plugin configuration:
- **Azure CNI with standard networking**: Default is 30 pods per node
- **Azure CNI with overlay networking**: Default is 250 pods per node  
- **kubenet**: Default is 110 pods per node

```yaml
spec:
  maxPods: 50  # Allow up to 50 pods per node
```

{{% alert title="Note" color="primary" %}}
With Azure CNI in standard mode, each pod gets an IP address from the subnet, so the maxPods setting is limited by available subnet IP addresses. With overlay networking, pods use a separate IP space allowing for much higher pod density.
{{% /alert %}}

## spec.kubelet

The `kubelet` section allows you to configure various kubelet parameters that affect node behavior. These settings are passed through to the kubelet configuration on each node.

### CPU Management

```yaml
spec:
  kubelet:
    cpuManagerPolicy: "static"  # or "none"
    cpuCFSQuota: true
    cpuCFSQuotaPeriod: "100ms"
```

- `cpuManagerPolicy`: Controls how the kubelet allocates CPU resources. Set to "static" for CPU pinning in latency-sensitive workloads.
- `cpuCFSQuota`: Enables CPU CFS quota enforcement for containers that specify CPU limits.
- `cpuCFSQuotaPeriod`: Sets the CPU CFS quota period.

### Image Garbage Collection

```yaml
spec:
  kubelet:
    imageGCHighThresholdPercent: 85
    imageGCLowThresholdPercent: 80
```

These settings control when the kubelet performs garbage collection of container images:
- `imageGCHighThresholdPercent`: Disk usage percentage that triggers image garbage collection
- `imageGCLowThresholdPercent`: Target disk usage percentage after garbage collection

### Topology Management

```yaml
spec:
  kubelet:
    topologyManagerPolicy: "best-effort"  # none, restricted, best-effort, single-numa-node
```

The topology manager helps coordinate resource allocation for latency-sensitive workloads across CPU and device (like GPU) resources.

### System Configuration

```yaml
spec:
  kubelet:
    allowedUnsafeSysctls: 
      - "kernel.msg*"
      - "net.ipv4.route.min_pmtu"
    containerLogMaxSize: "50Mi"
    containerLogMaxFiles: 5
    podPidsLimit: 4096
```

- `allowedUnsafeSysctls`: Whitelist of unsafe sysctls that pods can use
- `containerLogMaxSize`: Maximum size of container log files before rotation
- `containerLogMaxFiles`: Maximum number of container log files to retain
- `podPidsLimit`: Maximum number of processes allowed in any pod

## spec.tags

The `tags` field allows you to specify Azure resource tags that will be applied to all VM instances created using this AKSNodeClass. This is useful for cost tracking, resource organization, and compliance requirements.

```yaml
spec:
  tags:
    Environment: "production"
    Team: "platform"
    Application: "web-service"
    CostCenter: "engineering"
```

{{% alert title="Note" color="primary" %}}
Azure resource tags are key-value pairs with a limit of 50 tags per resource. Tag names are case-insensitive but tag values are case-sensitive. Some tag names are reserved by Azure and cannot be used.
{{% /alert %}}

## Examples

### GPU-Enabled NodeClass

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: gpu-nodeclass
spec:
  imageFamily: Ubuntu2204
  osDiskSizeGB: 256
  maxPods: 30
  kubelet:
    topologyManagerPolicy: "single-numa-node"
  tags:
    workload-type: "gpu"
    cost-center: "ml-team"
```

### High-Density NodeClass

For workloads requiring many small pods, you can configure a NodeClass optimized for high pod density:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: high-density
spec:
  imageFamily: Ubuntu2204
  maxPods: 250
  osDiskSizeGB: 128
  kubelet:
    imageGCHighThresholdPercent: 90
    imageGCLowThresholdPercent: 85
    containerLogMaxSize: "10Mi"
    containerLogMaxFiles: 3
  tags:
    density: "high"
    workload-type: "microservices"
```

### Performance-Optimized NodeClass

For latency-sensitive workloads requiring CPU pinning and NUMA awareness:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: performance-optimized
spec:
  imageFamily: AzureLinux
  osDiskSizeGB: 512
  maxPods: 50
  kubelet:
    cpuManagerPolicy: "static"
    topologyManagerPolicy: "single-numa-node"
    podPidsLimit: 8192
  tags:
    performance-tier: "high"
    workload-type: "compute-intensive"
```