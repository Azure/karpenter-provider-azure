Table of contents:
- [Installation (self-hosted)](#installation-self-hosted)
  - [Install utilities](#install-utilities)
  - [Create a cluster](#create-a-cluster)
  - [Configure Helm chart values](#configure-helm-chart-values)
  - [Install Karpenter](#install-karpenter)
- [Setup Workshop Env](#setup-workshop-env)

## Envrionment Setup

Use cloud shell at azure

### Create a directory for the workshop 

Create directory, and add it to the path for installed tooling.

```bash
mkdir -p ~/environment/karpenter/bin
export PATH=$PATH:~/environment/karpenter/bin
```

### Install utilities

yq (required) - used by some of the scripts below
```bash
wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -O ~/environment/karpenter/bin/yq
chmod +x ~/environment/karpenter/bin/yq
```

Optional Tools:
* [aks-node-viewer](https://github.com/azure/aks-node-viewer) - used for tracking price, and other metrics of nodes
* [k9s](https://github.com/derailed/k9s?tab=readme-ov-file#installation) - terminal UI to interact with the Kubernetes clusters

## Installation (self-hosted)

This guide shows how to get started with Karpenter by creating an AKS cluster and installing Karpenter.

### Create a cluster

Create a new AKS cluster with the required configuration, and ready to run Karpenter using workload identity.

Set environment variables:

```bash
export CLUSTER_NAME=karpenter
export RG=karpenter
export LOCATION=westus3
export KARPENTER_NAMESPACE=kube-system
```

Select the subscription to use:

```bash
export AZURE_SUBSCRIPTION_ID=<personal-azure-sub>
az account set --subscription ${AZURE_SUBSCRIPTION_ID}
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
export KARPENTER_VERSION=0.7.0

# Download the specific's version template
curl -sO https://raw.githubusercontent.com/Azure/karpenter/v${KARPENTER_VERSION}/karpenter-values-template.yaml

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

## Setup Workshop Env

### Setup a workshop namespace

```bash
kubectl create namespace workshop
```

