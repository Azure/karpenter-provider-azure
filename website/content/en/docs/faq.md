---
title: "FAQ"
linkTitle: "FAQ"
weight: 60
description: >
  Frequently asked questions about Karpenter for Azure
---

## General Questions

### What is Karpenter for Azure?
Karpenter for Azure is a node lifecycle management solution for Azure Kubernetes Service (AKS) clusters. It automatically provisions right-sized nodes in response to unschedulable pods and removes nodes when they're no longer needed.

### How does Karpenter for Azure differ from the Cluster Autoscaler?
- **Direct VM management**: Karpenter provisions VMs directly rather than scaling VM Scale Sets
- **Faster provisioning**: No need to pre-create VM Scale Sets for every combination of instance type and zone
- **Better bin packing**: Considers multiple instance types and zones simultaneously
- **More flexible**: Supports diverse instance types, zones, and capacity types without complex configuration

### Is Karpenter for Azure production ready?
Karpenter for Azure is actively developed and used in production environments. However, always test thoroughly in your specific environment before production deployment.

## Installation and Configuration

### What permissions does Karpenter need in Azure?
Karpenter requires permissions to:
- Create and delete Virtual Machines
- Create and delete Network Interfaces  
- Read Virtual Network and Subnet information
- Read VM sizes and availability
- Manage Azure resource tags
- Access spot VM pricing information

### Can I use Karpenter with existing AKS clusters?
Yes, Karpenter can be installed on existing AKS clusters. However, ensure you disable cluster autoscaler and understand how it will interact with existing autoscaling solutions to avoid conflicts.

### Do I need to remove Cluster Autoscaler before installing Karpenter?
It's recommended to remove or disable Cluster Autoscaler to avoid conflicts. Eventually our team may support this path but for now its blocked.

## Node Management

### Which Azure VM sizes does Karpenter support?
Karpenter supports most Azure VM sizes that are:
- Available in AKS
- Have 2 or more vCPUs
- Support standard Azure managed disks
- Are available in your region and availability zones


### How does Karpenter handle spot VMs?
Karpenter supports Azure Spot VMs for cost savings. When using spot instances, be aware that they may be reclaimed by Azure when capacity is needed elsewhere.

### Can I mix spot and regular VMs in the same NodePool?
Yes, you can specify both `spot` and `on-demand` in the capacity type requirements. Karpenter will prefer spot instances when available.

## Networking and Security

### What networking configurations does Karpenter support?
Karpenter supports:
- Azure CNI with subnet IP allocation
- Azure CNI with overlay networking
- Custom VNet configurations
- Bring Your Own CNI (BYO CNI) configurations

### How does Karpenter handle network security groups?
Karpenter uses the network security groups configured for your AKS cluster. It doesn't create or modify NSG rules.

### Can I use private AKS clusters with Karpenter?
Karpenter should work with private AKS clusters but has limited testing, so use at your own discretion. Ensure the Karpenter controller can reach Azure APIs through private endpoints or NAT gateway.

### What is the support policy for Bring Your Own CNI (BYO CNI)?
Karpenter supports BYO CNI configurations following the same support policy as AKS:

**Supported**: Karpenter-specific issues when using BYO CNI (node provisioning, scaling, lifecycle management)

**Not Supported**: CNI-specific networking issues, configuration problems, or troubleshooting third-party CNI plugins

If you encounter networking issues while using BYO CNI, first determine whether the problem is Karpenter-specific or CNI-related. For CNI-specific issues, contact your CNI vendor or community support channels. For Karpenter integration issues, contact Karpenter support.

## Scaling and Performance


### What's the maximum number of nodes Karpenter can manage?
Karpenter can manage thousands of nodes, but practical limits depend on:
- AKS cluster limits (typically 1000-5000 nodes)
- Azure subscription quotas
- Network subnet IP address availability

### How does Karpenter decide which nodes to terminate?
Karpenter considers several factors:
- Node utilization and efficiency
- Pod disruption budgets
- Node age and expiration settings
- Cost optimization opportunities

## Troubleshooting

### Why aren't my pods being scheduled?
Common reasons include:
- No NodePool matches the pod's requirements
- NodePool limits have been reached
- Insufficient Azure quota or capacity
- Pod has unsatisfiable constraints

Check: `kubectl describe pod <pod-name>` and `kubectl logs -n karpenter deployment/karpenter`

### Why is Karpenter not terminating underutilized nodes?
Possible causes:
- Pods without proper tolerations
- DaemonSets preventing node drain
- Pod disruption budgets blocking eviction
- Nodes marked with `do-not-disrupt` annotation

### How can I debug Karpenter issues?
1. Examine NodePool and AKSNodeClass status:
   ```bash
   kubectl describe nodepool <name>
   kubectl describe aksnodeclass <name>
   ```

2. Check NodeClaim status for provisioning failures:
   ```bash
   kubectl describe nodeclaim <name>
   ```
   Note: The NodeClaim messages sometimes contain details about why previous provisioning attempts failed.

3. Review node and pod events:
   ```bash
   kubectl get events --sort-by='.lastTimestamp'
   ```

## Cost Optimization

### How much can Karpenter save compared to static node groups?
Savings vary by workload but typically range from 20-60% through:
- Right-sizing instances to actual needs
- Using spot instances when appropriate  
- Removing idle capacity quickly
- Better bin packing efficiency

### Does Karpenter support Azure Reserved Instances?
Karpenter can provision VMs that benefit from Reserved Instance pricing, but it doesn't directly manage reservations. Purchase reservations for your expected baseline capacity. You can configure a NodePool with higher weights and only the instance types from the reserved instances until you hit quota limits, then once that NodePool's instance types are exhausted it will fall back onto on-demand.

### How can I optimize costs with Karpenter?
- Use spot instances for fault-tolerant workloads
- Set appropriate expiration times for security updates
- Configure consolidation policies
- Use resource limits to prevent unexpected scaling
- Monitor and tune your NodePool configurations

## Integration and Compatibility


### Does Karpenter work with Azure Policy?
Yes, VMs provisioned by Karpenter are subject to Azure Policy rules applied to the resource group and subscription.

### Can I use Karpenter with GitOps tools?
Yes, Karpenter resources (NodePools, AKSNodeClasses) can be managed through GitOps tools like ArgoCD or Flux.

### Does Karpenter support Windows nodes?
Karpenter primarily focuses on Linux nodes. Windows node support is estimated to be ready by the end of 2025.

## Getting Help

### Where can I find more information?
- [Karpenter Azure Documentation](https://azure.github.io/karpenter-provider-azure/)
- [GitHub Repository](https://github.com/Azure/karpenter-provider-azure)
- [Kubernetes Slack #azure-provider](https://kubernetes.slack.com/channels/azure-provider)

### How do I report bugs or request features?
Create an issue in the [GitHub repository](https://github.com/Azure/karpenter-provider-azure/issues) with:
- Detailed description of the issue
- Steps to reproduce
- Karpenter version and configuration
- Relevant logs and error messages

### Is there commercial support available?
Yes, commercial support is offered through Azure support channels, so long as you are using the managed offering NAP rather than self hosted.
