# Azure Karpenter

[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/Azure/karpenter-provider-azure/blob/main/LICENSE)
[![CI](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml)
![GitHub stars](https://img.shields.io/github/stars/Azure/karpenter-provider-azure)
![GitHub forks](https://img.shields.io/github/forks/Azure/karpenter-provider-azure)
[![Coverage Status](https://coveralls.io/repos/github/Azure/karpenter-provider-azure/badge.svg?branch=main)](https://coveralls.io/github/Azure/karpenter-provider-azure?branch=main)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/Azure/karpenter-provider-azure/issues)

## Overview

To handle your compute workloads efficiently, Karpenter takes away your responsibility of managing machines orchestration and lifecycle, optimizing for compute resources utilization rate and overall cost.

## How It Works

Once configured, Karpenter enables automatic node provisioning and management on your Kubernetes cluster by:

- **Watching** for pods that the Kubernetes scheduler has marked as unschedulable
- **Evaluating** scheduling constraints (resource requests, node selectors, affinities, tolerations, and topology-spread constraints) requested by the pods
- **Provisioning** cheapest possible nodes that meet the requirements of the pods
- **Removing** the nodes when they are no longer needed
- **Consolidating** existing nodes arrangement onto a cheaper arrangement with higher utilization per node

See more details on functionality and configuration options in [Karpenter documentation](https://karpenter.sh/docs/concepts/).

## Installation

Azure Karpenter works with an [Azure Kubernetes Services (AKS)](https://azure.microsoft.com/products/kubernetes-service) cluster through several deployment options:

### Node Auto Provisioning (NAP)

Karpenter is deployed by AKS as a managed service, similar to managed Cluster Autoscaler. This is the recommended option for most users, coming with perks such as:

- Hands-free management. Examples of duties relieved include:
  - Token rotations
  - Deployment charts management
  - Karpenter version updates
  - VM OS disk updates
  - Node image upgrades
- Official Microsoft/AKS support channels and service level agreements
  - Self-hosted Karpenter is supported only via GitHub issues, and subject to best-effort response time.
- More AKS features integration, such as maintenance windows
- Better integration test coverage

#### Quick Start

```bash
az aks update --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --node-provisioning-mode Auto
```

See [Microsoft Learn documenation](https://learn.microsoft.com/azure/aks/use-node-auto-provisioning?pivots=azure-cli) for more details and configurations.

---

### AKS Automatic

AKS Automatic is built on top of NAP, taking a step further and in automating many of AKS cluster management tasks away from cluster admins.
<!-- TODO: More docs. -->

```bash
az aks create --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --sku automatic --enable-hosted-system
```

See [Microsoft Learn documentation](https://learn.microsoft.com/azure/aks/automatic/quick-automatic-managed-network?pivots=azure-cli) for more details and configurations.

---

### Self-Hosted Karpenter

Karpenter is run as a standalone deployment in the cluster. This mode is useful for advanced users who want to customize or experiment with Karpenter's deployment, use custom Helm charts, or integrate non-standard workflows. Self-hosted mode requires users to directly manage upgrades, token rotation, and helm charts.

See [installation instructions](docs/self-hosted-karpenter.md).
<!-- TODO: Makefile stuff. -->

#### Non-AKS Kubernetes on Azure

Karpenter may work on it with additional modifications, but it is not yet a maintained path.

## Community, Discussion, and Support

- [Karpenter documentation](https://karpenter.sh/docs/concepts/) — more on Karpenter's concepts and functionalities
- [Microsoft Learn documentation](https://learn.microsoft.com/azure/aks/node-autoprovision?tabs=azure-cli) — more on AKS Node Auto Provisioning (NAP)
  - [Known limitations and unsupported features](https://learn.microsoft.com/azure/aks/node-auto-provisioning?tabs=azure-cli#limitations-and-unsupported-features)
- [#karpenter](https://kubernetes.slack.com/archives/C02SFFZSA2K) channel in the [Kubernetes slack](https://slack.k8s.io/)
- [Adopters list](https://github.com/Azure/karpenter-provider-azure/blob/main/ADOPTERS.md) — for wider discussion opportunities, consider adding your organization or subdivision

This open-source project is subject to best-effort support by project maintainers and not covered by Microsoft support channels. [File an issue](https://github.com/Azure/karpenter-provider-azure/issues) in this project for any questions, feature requests, or issues.

## Contribution

Check out [contributions guide](https://github.com/Azure/karpenter-provider-azure/blob/main/CONTRIBUTING.md) and [open source maintainship in the age of AI](https://kubernetes.io/blog/2026/06/26/open-source-maintainership-in-the-age-of-ai/) (new).

Azure Karpenter (this project) implements [kubernetes-sigs/karpenter](https://github.com/kubernetes-sigs/karpenter) ("Karpenter Core"), integrating it with Azure infrastructure and AKS-specific features. Capabilities that can be shared with other cloud providers should be contributed to the core project.

### Code of Conduct

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/)
or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

### Source Attribution

Many thanks to @ellistarn, @jonathan-innis, @tzneal, @bwagner5, @njtran, and many other developers active in the Karpenter community for laying the foundations of a Karpenter provider ecosystem!

Many thanks to @tallaxes, @Bryce-Soghigian, @rakechill, @charliedmcb, @jackfrancis, @comtalyst, @aagusuab, @matthchr, @gandhipr, @dtzar for laying the foundations of Azure Karpenter Provider!