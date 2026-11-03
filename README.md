# Azure Karpenter

[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/Azure/karpenter-provider-azure/blob/main/LICENSE)
[![CI](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml)
![GitHub stars](https://img.shields.io/github/stars/Azure/karpenter-provider-azure)
![GitHub forks](https://img.shields.io/github/forks/Azure/karpenter-provider-azure)
[![Coverage Status](https://coveralls.io/repos/github/Azure/karpenter-provider-azure/badge.svg?branch=main)](https://coveralls.io/github/Azure/karpenter-provider-azure?branch=main)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/Azure/karpenter-provider-azure/issues)

To handle your compute workloads efficiently, [Azure Kubernetes Services (AKS)](https://azure.microsoft.com/products/kubernetes-service) + Karpenter takes away your responsibility of managing machine provisioning and their lifecycle, while optimizing for your **compute infrastructure cost** and **utilization rate of compute resources**.

Azure Karpenter implements [Karpenter core](https://github.com/kubernetes-sigs/karpenter), integrating with Azure infrastructure and AKS-specific features.

## Features Overview

Once configured, Karpenter enables automatic node provisioning and management on your cluster by:

- **Watching** for pods that the Kubernetes scheduler has marked as unschedulable
- **Evaluating** scheduling constraints (resource requests, node selectors, affinities, tolerations, and topology-spread constraints) requested by the pods
- **Provisioning** cheapest possible nodes that meet the requirements of the pods
- **Removing** the nodes when they are no longer needed
- **Consolidating** existing nodes arrangement onto a cheaper arrangement with higher utilization per node

See more details on functionality in [Karpenter documentation](https://karpenter.sh/docs/concepts/).

## Installation

Karpenter on AKS comes with several deployment options:

### Node Auto Provisioning (NAP)

Karpenter is deployed by AKS as a managed service, similar to [managed Cluster Autoscaler](https://learn.microsoft.com/azure/aks/cluster-autoscaler?tabs=azure-cli). This is the recommended option for most users, coming with perks such as:

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

See [documentation](docs/self-hosted-karpenter.md) for more installation instructions.
<!-- TODO: Makefile stuff. -->
Self-hosted Kubernetes on non-AKS Azure infrastructures may work with additional modifications, but not yet a maintained path.

## Known Limitations

See [limitations and unsupported features](https://learn.microsoft.com/azure/aks/node-auto-provisioning?tabs=azure-cli#limitations-and-unsupported-features).

## Source Attribution

Many thanks to @ellistarn, @jonathan-innis, @tzneal, @bwagner5, @njtran, and many other developers active in the Karpenter community for laying the foundations of a Karpenter provider ecosystem!

Many thanks to @tallaxes, @Bryce-Soghigian, @rakechill, @charliedmcb, @jackfrancis, @comtalyst, @aagusuab, @matthchr, @gandhipr, @dtzar for laying the foundations of Azure Karpenter Provider!

## Contribution

Check out our [contributions guide](https://github.com/Azure/karpenter-provider-azure/blob/main/CONTRIBUTING.md).

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/)
or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

## Learn More

Check out the [Karpenter documentation](https://karpenter.sh/docs/concepts/) and [Microsoft Learn documentation for NAP on AKS](https://learn.microsoft.com/azure/aks/node-autoprovision?tabs=azure-cli) to learn more.

### Community, Discussion, and Support

Come discuss Karpenter in the [#karpenter](https://kubernetes.slack.com/archives/C02SFFZSA2K) channel in the [Kubernetes slack](https://slack.k8s.io/)!

Check out our [adopters list](https://github.com/Azure/karpenter-provider-azure/blob/main/ADOPTERS.md) to see other users. Please consider adding your organization or subdivision if you are using the project.

This open-source project is subject to best-effort support by project maintainers and not covered by Microsoft support channels. [File an issue](https://github.com/Azure/karpenter-provider-azure/issues) in this project for any questions, feature requests, or issues.
