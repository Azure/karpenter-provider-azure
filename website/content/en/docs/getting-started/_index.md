---
title: "Getting Started"
linkTitle: "Getting Started"
weight: 10
description: >
  Get started with Karpenter for Azure using NAP or self-hosted deployment
---

Karpenter for Azure automatically provisions right-sized nodes for your AKS workloads, improving efficiency and reducing costs. Choose between two deployment modes to get started.

## Quick Start

Follow our [Quickstart Guide](quickstart/) for step-by-step instructions to deploy Karpenter on your AKS cluster in minutes.

## Deployment Modes

### Node Auto Provisioning (NAP) - Recommended

The managed approach where AKS handles Karpenter as an addon:
- **Zero operational overhead** - Microsoft manages everything
- **Enterprise support** - Covered under Azure support agreements  
- **Automatic updates** - Updates with AKS releases
- **Best for**: Production workloads requiring minimal management

### Self-hosted Mode

For advanced users who need full control:
- **Complete customization** - Configure all aspects of Karpenter
- **Latest features** - Access newest Karpenter capabilities immediately
- **Full control** - Manage deployment, updates, and configuration
- **Best for**: Development, testing, and advanced customization needs

Learn more about [deployment modes](../concepts/deployment-modes/) to choose the right approach.

## Alternative Installation Methods

### Terraform

Use Terraform to automate your infrastructure:
* [Azure Terraform Modules](https://registry.terraform.io/modules/Azure/aks/azurerm/latest): Create AKS clusters with Terraform
* [Karpenter Terraform Examples](https://github.com/Azure-Samples/aks-karpenter-terraform): Complete infrastructure as code examples

### ARM Templates

Deploy using Azure Resource Manager templates:
* Available in the [quickstart guide](quickstart/#arm-template-example) for NAP deployment
* Infrastructure as code approach for reproducible deployments

## Learning Resources

Enhance your Karpenter knowledge:

* **[AKS Best Practices](https://docs.microsoft.com/en-us/azure/aks/best-practices)** - General AKS guidance
* **[Azure Spot VMs Workshop](https://github.com/Azure-Samples/azure-spot-vms-workshop)** - Cost optimization with spot instances
* **[AKS Karpenter Workshop](https://github.com/Azure-Samples/aks-karpenter-workshop)** - Hands-on Karpenter training

## What's Next?

After completing the quickstart:

1. **Understand the concepts**: Learn about [NodePools](../concepts/nodepools/), [AKSNodeClasses](../concepts/nodeclasses/), and [disruption policies](../concepts/disruption/)
2. **Explore advanced features**: Configure [GPU workloads](../tasks/managing-vm-images/#gpu-images), [spot instances](../concepts/nodepools/#spot-instances), and [custom images](../tasks/managing-vm-images/)
3. **Prepare for production**: Review [monitoring](../observability/), [troubleshooting](../troubleshooting/), and [upgrade procedures](../upgrading/)

## Getting Help

- **Community**: Join [#karpenter](https://kubernetes.slack.com/archives/C02SFFZSA2K) in Kubernetes Slack
- **Issues**: Report bugs on [GitHub](https://github.com/Azure/karpenter-provider-azure/issues)
- **Documentation**: Browse the complete [documentation](../)