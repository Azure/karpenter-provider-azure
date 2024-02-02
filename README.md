[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/Azure/karpenter-provider-azure/blob/main/LICENSE.txt)
[![CI](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml)
![GitHub stars](https://img.shields.io/github/stars/Azure/karpenter-provider-azure)
![GitHub forks](https://img.shields.io/github/forks/Azure/karpenter-provider-azure)
[![Go Report Card](https://goreportcard.com/badge/github.com/Azure/karpenter-provider-azure)](https://goreportcard.com/report/github.com/Azure/karpenter-provider-azure)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/Azure/karpenter-provider-azure/issues)

---
## Features Overview 
The AKS Karpenter Provider enables node autoprovisioning using [Karpenter](https://karpenter.sh/) on your AKS cluster.
Karpenter improves the efficiency and cost of running workloads on Kubernetes clusters by:

* **Watching** for pods that the Kubernetes scheduler has marked as unschedulable
* **Evaluating** scheduling constraints (resource requests, node selectors, affinities, tolerations, and topology-spread constraints) requested by the pods
* **Provisioning** nodes that meet the requirements of the pods
* **Removing** the nodes when they are no longer needed
* **Consolidating** existing nodes onto cheaper nodes with higher utilization per node

## Production Readiness Status
- API-Version: AKS Karpenter Provider is currently in alpha (`v1alpha2`).
- AKS-Feature State: [Node Auto Provisioning is currently in preview](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli)

## Installation: Managed Karpenter (AKA Node Auto Provisioning) 
The Node Auto Provisioning Preview, runs Karpenter as a managed addon similar to Managed Cluster Autoscaler. 

To get started, just go through the prerequisites of [installing the Preview CLI](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli#install-the-aks-preview-cli-extension), and [register the NodeAutoProvisioningPreview feature flag](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli#register-the-nodeautoprovisioningpreview-feature-flag).

### Enable node autoprovisioning 
To enable node autoprovisioning, create a new cluster using the `az aks create` command and set --node-provisioning-mode to "Auto". You'll also need to use overlay networking and the cilium network policy for now.

```bash 
az aks create --name myFirstNap --resource-group napTest --node-provisioning-mode Auto --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium
```

[View Limitations of the node autoprovisioning preview here](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli#limitations)

### NAP Usage
- [Learn More about Configuring the Nodepool CRDs to be more or less flexible](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli#node-pools)

## Helm Chart
a self-hosted experience similar to [aws/karpenter](https://artifacthub.io/packages/helm/karpenter/karpenter) is coming soon...

---

### Source Attribution

Notice: Files in this source code originated from a fork of https://github.com/aws/karpenter
which is under an Apache 2.0 license. Those files have been modified to reflect environmental requirements in AKS and Azure.

Many thanks to @ellistarn, @jonathan-innis, @tzneal, @bwagner5, @njtran, and many other developers active in the Karpenter community for laying the foundations of a Karpenter provider ecosystem!

Many thanks to @Bryce-Soghigian, @rakechill, @charliedmcb, @jackfrancis, @comtalyst, @aagusuab, @matthchr, @gandhipr, @dtzar for contributing to AKS Karpenter Provider!

---
### Community, discussion, contribution, and support 
This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/)
or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

Come discuss Karpenter in the [#karpenter](https://kubernetes.slack.com/archives/C02SFFZSA2K) channel in the [Kubernetes slack](https://slack.k8s.io/)!

Check out the [Docs](https://karpenter.sh/) to learn more.

Check out our [contributing guide](https://github.com/Azure/karpenter-provider-azure/blob/main/CONTRIBUTING.md).