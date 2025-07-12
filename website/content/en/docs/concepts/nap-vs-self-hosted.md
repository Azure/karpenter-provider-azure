---
title: "NAP vs Self-Hosted"
linkTitle: "NAP vs Self-Hosted" 
weight: 50
description: >
  Understanding Node Auto Provisioning (NAP) vs Self-hosted deployment options
---

Karpenter for Azure offers two distinct deployment options to accommodate different user needs and preferences. Understanding these options will help you choose the right approach for your AKS cluster.

## Overview

Karpenter provider for AKS can be deployed in two modes:

- **Node Auto Provisioning (NAP)**: A managed service where AKS runs Karpenter as an addon
- **Self-hosted**: A standalone deployment where you manage Karpenter directly in your cluster

## Node Auto Provisioning (NAP) Mode

### What is NAP?
[Node Auto Provisioning (NAP)](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli) is a managed service where AKS automatically deploys, configures, and manages Karpenter on your cluster. This is the **recommended mode for most users**.

NAP uses pending pod resource requirements to decide the optimal virtual machine configuration to run workloads in the most efficient and cost-effective manner. It automatically manages the entire Karpenter lifecycle as a managed AKS addon.

### Benefits of NAP
- **Fully managed**: Microsoft manages the Karpenter deployment, updates, and lifecycle
- **Zero operational overhead**: No need to install, configure, or maintain Karpenter
- **Automatic updates**: Karpenter is automatically updated with AKS cluster updates and images
- **Enterprise support**: Covered under Azure support agreements
- **Integrated monitoring**: Built-in integration with Azure Monitor, grafana, metrics, and logging
- **Security**: We manage the bootstrapping token for you 
- **Cost optimization**: Automatically provisions optimal VM configurations for workloads

### When to Use NAP

Choose NAP when:
- You want a production-ready solution with minimal operational overhead
- You prefer managed services over self-managed deployments
- You need enterprise support and SLA coverage
- You want automatic updates and security patches
- You don't need deep customization of Karpenter configuration
- You want to optimize costs automatically

### NAP + Self-Hosted Limitations

**Networking Requirements:**
The recommended network configurations for NAP are:
- Azure CNI Overlay with Powered by Cilium (recommended)
- Azure CNI Overlay
- Azure CNI with Powered by Cilium
- Azure CNI

**Unsupported features:**
- Windows node pools
- Custom kubelet configuration
- IPv6 clusters
- Service Principals (use managed identity instead)
- Disk encryption sets
- CustomCATrustCertificates
- Start/Stop mode
- HTTP proxy
- OutboundType mutation after creation
- Private cluster with custom private DNS
- Calico network policy
- Dynamic IP Allocation
- Static Allocation of CIDR blocks

### Using NAP

#### Create a new cluster with NAP

```bash
# Set environment variables
export CLUSTER_NAME=myCluster
export RESOURCE_GROUP_NAME=myResourceGroup

# Create cluster with NAP enabled
az aks create \
  --name $CLUSTER_NAME \
  --resource-group $RESOURCE_GROUP_NAME \
  --node-provisioning-mode Auto \
  --network-plugin azure \
  --network-plugin-mode overlay \
  --network-dataplane cilium \
  --generate-ssh-keys
```

#### Enable NAP on existing cluster

```bash
# Enable NAP on existing cluster
az aks update \
  --name $CLUSTER_NAME \
  --resource-group $RESOURCE_GROUP_NAME \
  --node-provisioning-mode Auto \
  --network-plugin azure \
  --network-plugin-mode overlay \
  --network-dataplane cilium
```

#### ARM Template Example

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "resources": [
    {
      "type": "Microsoft.ContainerService/managedClusters",
      "apiVersion": "2023-09-02-preview",
      "name": "napcluster",
      "location": "westus2",
      "identity": {
        "type": "SystemAssigned"
      },
      "properties": {
        "networkProfile": {
          "networkPlugin": "azure",
          "networkPluginMode": "overlay",
          "networkPolicy": "cilium",
          "networkDataplane": "cilium",
          "loadBalancerSku": "Standard"
        },
        "dnsPrefix": "napcluster",
        "agentPoolProfiles": [
          {
            "name": "agentpool",
            "count": 3,
            "vmSize": "standard_d2s_v3",
            "osType": "Linux",
            "mode": "System"
          }
        ],
        "nodeProvisioningProfile": {
          "mode": "Auto"
        }
      }
    }
  ]
}
```

## Self-hosted Mode

### What is Self-hosted Mode?

Self-hosted mode runs Karpenter as a regular Kubernetes deployment in your cluster, giving you full control over its configuration and lifecycle. You probably don't want this, as that comes with managing the bootstrap token and other headaches. It is our belief that you should use NAP and only use self hosted for experimentation with new feature flags or development of karpenter. 

This will change in the future as we add more stability and parity to the two projects.

### Self-hosted Limitations

- **Operational overhead**: You manage installation, updates, and maintenance
- **Support responsibility**: You're responsible for troubleshooting and support
- **Security management**: You must configure identity, RBAC, and security settings
- **Update management**: Manual process to update Karpenter versions

### Self-hosted Architecture

In self-hosted mode:

In self-hosted mode, Karpenter is deployed directly into your AKS cluster as a standard Kubernetes deployment—typically in the `kube-system` or a dedicated namespace. This means Karpenter runs alongside your workloads and other cluster components, giving you direct access to its configuration, logs, and lifecycle management.

By contrast, in Node Auto Provisioning (NAP) mode, Karpenter runs as a managed addon within the AKS control plane, alongside the Kubernetes API server and other critical system components. In this managed scenario, you do not interact directly with the Karpenter deployment; Microsoft manages its lifecycle, configuration, and upgrades for you.

**Key architectural difference:**  
- **Self-hosted:** Karpenter runs in your overlay (user) cluster, managed by you.
- **NAP:** Karpenter runs in the managed AKS control plane, managed by Azure.

This distinction affects how you interact with Karpenter, what you can customize, and who is responsible for its operation and security.
  
### Installing Self-hosted Karpenter
See an installation guide [here](https://github.com/Azure/karpenter-provider-azure?tab=readme-ov-file#installation-self-hosted).

### Features Available in NAP but Not Self-hosted

NAP mode provides several features and capabilities that are not available in self-hosted mode:

- **v6 SKUs**: Only supported in NAP mode. Self-hosted mode does not support v6 generation VM SKUs.
- **--dns-service-ip**: Only supported in NAP mode for now.
- **Automatic bootstrap token management**: NAP handles node bootstrapping tokens automatically, if you are using self-hosted,
you will be managing that token yourself after each upgrade to the systempool or change.
- **Service CIDR**: Only supported or honored in NAP
- **Integrated monitoring**: Built-in Azure Monitor integration with predefined metrics and dashboards
- **Automatic security updates**: Security patches and updates are managed by Azure
- **Enterprise support**: Covered under Azure support SLAs and agreements feel free to file a support ticket 

**Advanced Capabilities:**
- **Optimized provisioning**: NAP uses Azure-specific optimizations for faster node provisioning
- **Regional feature rollouts**: New Azure features are often available in NAP before self-hosted
- **Cross-region failover**: Built-in support for multi-region deployments

## Comparison Matrix
| Feature | NAP Mode | Self-hosted Mode |
|---------|----------|------------------|
| **Management** | Fully managed by Microsoft | User managed |
| **Installation** | Single CLI command | Multi-step setup process |
| **Updates** | Automatic | Manual |
| **Support** | Azure enterprise support | Community + self-support |
| **Latest features** | Follows AKS release cycle | Immediate access |
| **Operational overhead** | Minimal | High |
| **Production readiness** | Enterprise ready | Use at your own risk |
| **Cost** | Included in AKS | Self-managed infrastructure |


With self-hosted karpenter, you will be responsible for managing the node bootstrapping token, which is a very expensive and dangerous thing to manage yourself. 

## Migration Between Modes

### From Self-hosted to NAP

1. **Backup current configuration**:
```bash
kubectl get nodepools,aksnodeclasses -o yaml > karpenter-backup.yaml
```

2. **Uninstall self-hosted Karpenter**:
```bash
helm uninstall karpenter -n kube-system
```

Delete those CRDs + CRs, note you will need to scale down everything for the migration

3. **Enable NAP**:
```bash
az aks update --resource-group myRG --name myCluster --node-provisioning-mode Auto
```

4. **Recreate NodePools** (configuration may need adjustment for NAP compatibility)

### From NAP to Self-hosted
This configuration is currently untested and unsupported, not to say its impossible just not throughly thought through