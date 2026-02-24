
[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/Azure/karpenter-provider-azure/blob/main/LICENSE.txt)
[![CI](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Azure/karpenter-provider-azure/actions/workflows/ci.yml)
![GitHub stars](https://img.shields.io/github/stars/Azure/karpenter-provider-azure)
![GitHub forks](https://img.shields.io/github/forks/Azure/karpenter-provider-azure)
[![Go Report Card](https://goreportcard.com/badge/github.com/Azure/karpenter-provider-azure)](https://goreportcard.com/report/github.com/Azure/karpenter-provider-azure)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/Azure/karpenter-provider-azure/issues)

Table of contents:
- [Features Overview](#features-overview)
- [Node Auto Provisioning (NAP) vs. Self-hosted](#node-auto-provisioning-nap-vs-self-hosted)
- [Known limitations](#known-limitations)
- [Installation (self-hosted)](#installation-self-hosted)
  - [Install utilities](#install-utilities)
  - [Create a cluster](#create-a-cluster)
  - [Configure Helm chart values](#configure-helm-chart-values)
  - [Install Karpenter](#install-karpenter)
- [Using Karpenter (self-hosted)](#using-karpenter-self-hosted)
  - [Create NodePool](#create-nodepool)
  - [Scale up deployment](#scale-up-deployment)
  - [Scale down deployment](#scale-down-deployment)
  - [Delete Karpenter nodes manually](#delete-karpenter-nodes-manually)
- [Cleanup (self-hosted)](#cleanup-self-hosted)
  - [Delete the cluster](#delete-the-cluster)
- [Source Attribution](#source-attribution)
- [Community, discussion, contribution, and support](#community-discussion-contribution-and-support)

## Features Overview

The AKS Karpenter Provider enables node autoprovisioning using [Karpenter](https://karpenter.sh/) on your AKS cluster.
Karpenter improves the efficiency and cost of running workloads on Kubernetes clusters by:

* **Watching** for pods that the Kubernetes scheduler has marked as unschedulable
* **Evaluating** scheduling constraints (resource requests, node selectors, affinities, tolerations, and topology-spread constraints) requested by the pods
* **Provisioning** nodes that meet the requirements of the pods
* **Removing** the nodes when they are no longer needed
* **Consolidating** existing nodes onto cheaper nodes with higher utilization per node


## Node Auto Provisioning (NAP) vs. Self-hosted Karpenter

Karpenter provider for AKS can be used in two modes:
* **[Node Auto Provisioning (NAP)](https://learn.microsoft.com/azure/aks/node-autoprovision?tabs=azure-cli) mode**: Karpenter is run by AKS as a managed addon similar to managed Cluster Autoscaler. This is the recommended mode for most users as it has more test coverage, improved scale-up speed, automatic maintenance window integration, support for some additional SKUs, and various other improvements over self-hosted Karpenter. NAP also leverages an AKS experience, Node Provisioning Service, which integrates Azure tools into the managed Karpenter experience for optimized performance and scalability. Follow the instructions in Node Auto Provisioning [documentation](https://learn.microsoft.com/azure/aks/node-autoprovision?tabs=azure-cli) to use Karpenter in that mode.
  * Token rotation
  * Helm charts
  * Kubernetes version upgrades
  * Karpenter version updates
  * VM OS disk updates
  * node image upgrades (Linux)
* **Self-hosted mode**: Karpenter is run as a standalone deployment in the cluster. This mode is useful for advanced users who want to customize or experiment with Karpenter's deployment, use custom Helm charts, or integrate non-standard workflows. Self-hosted mode requires users to directly manage upgrades, token rotation, and helm charts.

## Known limitations

The following AKS features are not supported:
* Windows nodes.
* Kubenet and Calico.
* IPv6 clusters.
* [Service Principal](https://learn.microsoft.com/azure/aks/kubernetes-service-principal) based clusters. A system-assigned or user-assigned managed identity must be used.
* Disk Encryption sets.
* Custom CA Certificates.
* [HTTP proxy](https://learn.microsoft.com/azure/aks/http-proxy).
* Clusters running Karpenter should not be [stopped](https://learn.microsoft.com/azure/aks/start-stop-cluster).
* All cluster egress [outbound types](https://learn.microsoft.com/azure/aks/egress-outboundtype) are supported, however the type can't be changed after the cluster is created.

## Installation (NAP)

Follow the instructions in the Node Auto Provisioning [documentation](https://learn.microsoft.com/azure/aks/node-autoprovision?tabs=azure-cli).

## Installation (self-hosted Karpenter)

**⚠️ Warning ⚠️** We strongly recommend using [Node Auto Provisioning](#installation-nap) (aka managed Karpenter) instead of self-hosted.

This guide shows how to get started with Karpenter by creating an AKS cluster and installing Karpenter.

### Install utilities

Install these tools before proceeding:
* [Azure CLI](https://docs.microsoft.com/cli/azure/install-azure-cli)
* [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/)
* [Helm](https://helm.sh/docs/intro/install/)
* [jq](https://stedolan.github.io/jq/) - used by some of the scripts below
* [yq](https://mikefarah.gitbook.io/yq/) - used by some of the scripts below

### Create a cluster

Create a new AKS cluster with the required configuration, and ready to run Karpenter using workload identity.

> Note: You can use `hack/deploy/create-cluster.sh <cluster-name> <resource-group> <namespace>` to automate the following steps.

Set environment variables:

```bash
export CLUSTER_NAME=karpenter
export RG=karpenter
export LOCATION=westus3
export KARPENTER_NAMESPACE=kube-system

```

Login and select a subscription to use:

```bash
az login
```

Create the resource group:

```bash
az group create --name ${RG} --location ${LOCATION}
```

Create the workload MSI that backs the karpenter pod auth:

```bash
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")
```

Create the AKS cluster compatible with Karpenter, with workload identity enabled:

```bash
AKS_JSON=$(az aks create \
  --name "${CLUSTER_NAME}" --resource-group "${RG}" \
  --node-count 3 --generate-ssh-keys \
  --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium \
  --enable-managed-identity \
  --enable-oidc-issuer --enable-workload-identity)
az aks get-credentials --name "${CLUSTER_NAME}" --resource-group "${RG}" --overwrite-existing
```

Create federated credential linked to the karpenter service account for auth usage:

```bash
az identity federated-credential create --name KARPENTER_FID --identity-name karpentermsi --resource-group "${RG}" \
  --issuer "$(jq -r ".oidcIssuerProfile.issuerUrl" <<< "$AKS_JSON")" \
  --subject system:serviceaccount:${KARPENTER_NAMESPACE}:karpenter-sa \
  --audience api://AzureADTokenExchange
```

Create role assignments to let Karpenter manage VMs and Network resources:

```bash
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(jq -r '.principalId' <<< "$KMSI_JSON")
RG_MC=$(jq -r ".nodeResourceGroup" <<< "$AKS_JSON")
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)
for role in "Virtual Machine Contributor" "Network Contributor" "Managed Identity Operator"; do
  az role assignment create --assignee "${KARPENTER_USER_ASSIGNED_CLIENT_ID}" --scope "${RG_MC_RES}" --role "$role"
done
```

> Note: If you experience any issues creating the role assignments, but should have the given ownership to do so, try going through the Azure portal:
> 1. Navigate to your MSI.
> 2. Give it the following roles "Virtual Machine Contributor", "Network Contributor", and "Managed Identity Operator" at the scope of the node resource group.

### Configure Helm chart values

The Karpenter Helm chart requires specific configuration values to work with an AKS cluster. While these values are documented within the Helm chart, you can use the `configure-values.sh` script to generate the `karpenter-values.yaml` file with the necessary configuration. This script queries the AKS cluster and creates `karpenter-values.yaml` using `karpenter-values-template.yaml` as the configuration template. Although the script automatically fetches the template from the main branch, inconsistencies may arise between the installed version of Karpenter and the repository code. Therefore, it is advisable to download the specific version of the template before running the script.

```bash
# Select version to install
export KARPENTER_VERSION=1.6.1

# Download the specific's version template
curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v${KARPENTER_VERSION}/karpenter-values-template.yaml

# use configure-values.sh to generate karpenter-values.yaml
# (in repo you can just do ./hack/deploy/configure-values.sh ${CLUSTER_NAME} ${RG})
curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v${KARPENTER_VERSION}/hack/deploy/configure-values.sh
chmod +x ./configure-values.sh && ./configure-values.sh ${CLUSTER_NAME} ${RG} karpenter-sa karpentermsi
```

### Install Karpenter

Using the generated `karpenter-values.yaml` file, install Karpenter using Helm:

```bash
helm upgrade --install karpenter oci://mcr.microsoft.com/aks/karpenter/karpenter \
  --version "${KARPENTER_VERSION}" \
  --namespace "${KARPENTER_NAMESPACE}" --create-namespace \
  --values karpenter-values.yaml \
  --set controller.resources.requests.cpu=1 \
  --set controller.resources.requests.memory=1Gi \
  --set controller.resources.limits.cpu=1 \
  --set controller.resources.limits.memory=1Gi \
  --wait
```

Check karpenter deployed successfully:

```bash
kubectl get pods --namespace "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter
```

Check its logs:

```bash
kubectl logs -f -n "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter -c controller
```

Note: Snapshot versions can be installed in a similar way for development:

```bash
export KARPENTER_NAMESPACE=kube-system
export KARPENTER_VERSION=0-f83fadf2c99ffc2b7429cb40a316fcefc0c4752a

helm upgrade --install karpenter oci://ksnap.azurecr.io/karpenter/snapshot/karpenter \
  --version "${KARPENTER_VERSION}" \
  --namespace "${KARPENTER_NAMESPACE}" --create-namespace \
  --values karpenter-values.yaml \
  --set controller.resources.requests.cpu=1 \
  --set controller.resources.requests.memory=1Gi \
  --set controller.resources.limits.cpu=1 \
  --set controller.resources.limits.memory=1Gi \
  --wait
```

## Using Karpenter (self-hosted)

### Create NodePool

A single Karpenter NodePool is capable of handling many different pod shapes. Karpenter makes scheduling and provisioning decisions based on pod attributes such as labels and affinity. In other words, Karpenter eliminates the need to manage many different node groups.

Create a default NodePool using the command below. (Additional examples available in the repository under [`examples/v1`](/examples/v1).) The `consolidationPolicy` set to `WhenUnderutilized` in the `disruption` block configures Karpenter to reduce cluster cost by removing and replacing nodes. As a result, consolidation will terminate any empty nodes on the cluster. This behavior can be disabled by setting consolidateAfter to `Never`, telling Karpenter that it should never consolidate nodes.

Note: This NodePool will create capacity as long as the sum of all created capacity is less than the specified limit.

```bash
cat <<EOF | kubectl apply -f -
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: general-purpose
  annotations:
    kubernetes.io/description: "General purpose NodePool"
spec:
  template:
    metadata:
      labels:
        # required for Karpenter to predict overhead from cilium DaemonSet
        kubernetes.azure.com/ebpf-dataplane: cilium
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: default
      startupTaints:
        # https://karpenter.sh/docs/concepts/nodepools/#cilium-startup-taint
        - key: node.cilium.io/agent-not-ready
          effect: NoExecute
          value: "true"
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: [D]
      expireAfter: Never
  limits:
    cpu: 100
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 0s
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: default
  annotations:
    kubernetes.io/description: "General purpose AKSNodeClass for running Ubuntu nodes"
spec:
  imageFamily: Ubuntu
EOF
```
Karpenter is now active and ready to begin provisioning nodes.

### Scale up deployment

This deployment uses the [pause image](https://www.ianlewis.org/en/almighty-pause-container) and starts with zero replicas.

```bash
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
spec:
  replicas: 0
  selector:
    matchLabels:
      app: inflate
  template:
    metadata:
      labels:
        app: inflate
    spec:
      terminationGracePeriodSeconds: 0
      containers:
        - name: inflate
          image: mcr.microsoft.com/oss/kubernetes/pause:3.6
          resources:
            requests:
              cpu: 1
EOF

kubectl scale deployment inflate --replicas 5
kubectl logs -f -n "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter -c controller
```

### Scale down deployment

Now, delete the deployment. After a short amount of time, Karpenter should terminate the empty nodes due to consolidation.

```bash
kubectl delete deployment inflate
kubectl logs -f -n "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter -c controller
```

### Delete Karpenter nodes manually

If you delete a node with kubectl, Karpenter will gracefully cordon, drain, and shutdown the corresponding instance. Under the hood, Karpenter adds a finalizer to the node object, which blocks deletion until all pods are drained and the instance is terminated. Keep in mind, this only works for nodes provisioned by Karpenter.

```bash
kubectl delete node $NODE_NAME
```

## Cleanup (self-hosted)

### Delete the cluster

To avoid additional charges, remove the demo infrastructure from your AKS account.

```bash
helm uninstall karpenter --namespace "${KARPENTER_NAMESPACE}"
az aks delete --name "${CLUSTER_NAME}" --resource-group "${RG}"
```

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
