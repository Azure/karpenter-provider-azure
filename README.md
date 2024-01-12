[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/Azure/karpenter/blob/main/LICENSE.txt)
[![CI](https://github.com/Azure/karpenter/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Azure/karpenter/actions/workflows/ci.yml)
![GitHub stars](https://img.shields.io/github/stars/Azure/karpenter)
![GitHub forks](https://img.shields.io/github/forks/Azure/karpenter)
[![Go Report Card](https://goreportcard.com/badge/github.com/Azure/karpenter)](https://goreportcard.com/report/github.com/Azure/karpenter)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/Azure/karpenter/issues)

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

## Development

A [GitHub Codespaces]((https://github.com/features/codespaces)) development flow is described below, which you can use to test karpenter functionality on your own cluster, and to aid rapid development of this project.

1. **Install VSCode**: Go [here](https://code.visualstudio.com/download) to download VSCode for your platform. After installation, in your VSCode app install the "GitHub Codespaces" Extension. See [here](https://code.visualstudio.com/docs/remote/codespaces) for more information about this extension.

2. **Create Codespace** (~2min): In browser, click Code / "Create a codespace on main" (for better experience customize to use 4cores/8GB), wait for codespace to be created. It is created with everything needed for development (Go, Azure CLI, kubectl, skaffold, useful plugins, etc.) Now you can open up the Codespace in VSCode: Click on Codespaces in the lower left corner in the browser status bar region, choose "Open in VSCode Desktop". (Pretty much everything except for `az login` and some `az role assignment` works in browser; but VSCode is a better experience anyway.)

More information on GitHub Codespaces is [here](https://github.com/features/codespaces).

3. **Provision cluster, build and deploy Karpenter** (~5min): Set `AZURE_SUBSCRIPTION_ID` to your subscription (and customize region in `Makefile-az.mk` if desired). Then at the VSCode command line run `make az-all`. This logs into Azure (follow the prompts), provisions AKS and ACR (using resource group `$CODESPACE_NAME`, so everything is unique / scoped to codespace), builds and deploys Karpenter, deploys sample `default` Provisioner and `inflate` Deployment workload.

Manually scale the `inflate` Deployment workload, watch Karpenter controller log and Nodes in the cluster. Explore further with `make help` (mostly `az-*` targets).

To debug Karpenter in-cluster, use `make az-debug`, wait for it to deploy, and attach from VSCode using Start Debugging (F5). After that you should be able to set breakpoints, examine variables, single step, etc. (Behind the scenes, besides building and deploying Karpenter, `skaffold debug` automatically and transparently applies the necessary flags during build, instruments the deployment with Delve, adjusts health probe timeouts - to allow for delays introduced by breakpoints, sets up port-forwarding, etc.; more on how this works is [here](https://skaffold.dev/docs/workflows/debug/).

Once done, you can delete all infra with `make az-rmrg` (it deletes the resource group), and can delete the codespace (though it will be automatically suspended when not used, and deleted after 30 days.)

#### Developer notes
- During step 1 you will observe `Running postCreateCommand...` which takes ~10+ minutes. You don't have to wait for it to finish to proceed to step 2.
- The following errors can be ignored during step 2:

```
ERRO[0007] gcloud binary not found
...
ERRO[0003] gcloud binary not found
...
ERRO[0187] walk.go:74: found symbolic link in path: /workspaces/karpenter/charts/karpenter/crds resolves to /workspaces/karpenter/pkg/apis/crds. Contents of linked file included and used  subtask=0 task=Render
```
- If you see platform architecture error during `skaffold debug`, adjust (or comment out) `--platform` argument.
- If you are not able to set/hit breakpoints, it could be an issue with source paths mapping; see comments in debug launch configuration (`launch.json`)

#### FAQs

Q: I was able to trigger Karpenter to execute scaling up nodes as expected, using my own customized deployment of pods. However, scaling down was not handled automatically when I removed the deployment. The two new nodes created by Karpenter were left around. What is going on?

A: Additional system workloads (such as metrics server) can get scheduled on the new nodes, preventing Karpenter from removing them. Note that you can always use `kubectl delete node <node>`, which will have Karpenter drain the node and terminate the instance from cloud provider.

Q: When running some of the tests locally, the environment failed to start. How can I resolve this?

A: Oftentimes, especially for pre-existing tests, running `make toolchain` will fix this. This target will ensure that you have the correct versions of binaries installed.

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
