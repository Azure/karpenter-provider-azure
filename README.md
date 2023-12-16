[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/Azure/karpenter/blob/main/LICENSE.txt)
[![CI](https://github.com/Azure/karpenter/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Azure/karpenter/actions/workflows/ci.yml)
![GitHub stars](https://img.shields.io/github/stars/Azure/karpenter)
![GitHub forks](https://img.shields.io/github/forks/Azure/karpenter)
[![Go Report Card](https://goreportcard.com/badge/github.com/Azure/karpenter)](https://goreportcard.com/report/github.com/Azure/karpenter)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/Azure/karpenter/issues)

---

# AKS Karpenter Provider

The AKS Karpenter Provider enables node autoprovisioning using [Karpenter](https://karpenter.sh/) on your AKS cluster.

The API for AKS Karpenter Provider is currently alpha (`v1alpha2`).

See the local development guide [website](#) or [local repo link](./website/content/en/docs/contributing/development-guide.md).

Karpenter is an open-source node provisioning project built for Kubernetes.
Karpenter improves the efficiency and cost of running workloads on Kubernetes clusters by:

* **Watching** for pods that the Kubernetes scheduler has marked as unschedulable
* **Evaluating** scheduling constraints (resource requests, nodeselectors, affinities, tolerations, and topology spread constraints) requested by the pods
* **Provisioning** nodes that meet the requirements of the pods
* **Removing** the nodes when the nodes are no longer needed

To learn more about karpenter generally, visit the [website](https://karpenter.sh/).

### Community

Come discuss Karpenter in the [#karpenter](https://kubernetes.slack.com/archives/C02SFFZSA2K) channel in the [Kubernetes slack](https://slack.k8s.io/)!

### FAQs

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

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/)
or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
