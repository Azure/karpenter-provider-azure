Table of contents:
- [Installation](#installation)
  - [Install utilities](#install-utilities)
  - [Create a cluster](#create-a-cluster)
  - [Configure Helm chart values](#configure-helm-chart-values)
  - [Install Karpenter](#install-karpenter)
  - [Create workshop namespace](#create-a-workshop-namespace)

## Envrionment Setup

### Launch the Cloud Shell Terminal

Open [https://shell.azure.com/](https://shell.azure.com/) in a new tab.

### Create a Directory for the Workshop 

Create the workshop's directory hierarchy, and add it's tooling bin to the path.

```bash
mkdir -p ~/environment/karpenter/bin
export PATH=$PATH:~/environment/karpenter/bin
```

### Install Utilities

Use the below command to install `yq` and `k9s`, both used for this workshop:

```bash
cd ~/environment/karpenter/bin

# yq - used by some of the scripts below
wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -O ~/environment/karpenter/bin/yq
chmod +x ~/environment/karpenter/bin/yq

# k9s - terminal UI to interact with the Kubernetes clusters
wget https://github.com/derailed/k9s/releases/download/v0.32.5/k9s_Linux_amd64.tar.gz -O ~/environment/karpenter/bin/k9s.tar.gz
tar -xf k9s.tar.gz
```

Optional Tools:
* [aks-node-viewer](https://github.com/azure/aks-node-viewer) - used for tracking price, and other metrics of nodes

## Installation

This guide shows how to get started with Karpenter by creating an AKS cluster and installing self-hosted Karpenter on it.

> Note: there is a managed version of Karpenter within AKS, called NAP (Node Autoprovisioning), with some more opinionated defaults and base scaling configurations. However, we'll be exploring the self-hosted approach today.

### Create a cluster

Create a new AKS cluster with the required configuration, and ready to run Karpenter using workload identity.

Select the subscription to use:

```bash
export AZURE_SUBSCRIPTION_ID=<personal-azure-sub>
az account set --subscription ${AZURE_SUBSCRIPTION_ID}
```

Set environment variables:

```bash
export CLUSTER_NAME=karpenter
export RG=karpenter
export LOCATION=westus3
export KARPENTER_NAMESPACE=kube-system
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

> Note: <br>
> \- If you see a warning for "CryptographyDeprecationWarning", "WARNING: SSH key files", and/or "WARNING: docker_bridge_cidr" these are not a concern, and can be disregarded. 

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

### Configure Helm chart values

The Karpenter Helm chart requires specific configuration values to work with an AKS cluster. While these values are documented within the Helm chart, you can use the `configure-values.sh` script to generate the `karpenter-values.yaml` file with the necessary configuration. This script queries the AKS cluster and creates `karpenter-values.yaml` using `karpenter-values-template.yaml` as the configuration template.

```bash
# Select version to install
export KARPENTER_VERSION=0.7.0

# move to the workshop folder as we are creating a few files
cd ~/environment/karpenter/

# Download the specific's version template
curl -sO https://raw.githubusercontent.com/Azure/karpenter/v${KARPENTER_VERSION}/karpenter-values-template.yaml

# use configure-values.sh to generate karpenter-values.yaml
# (in repo you can just do ./hack/deploy/configure-values.sh ${CLUSTER_NAME} ${RG})
curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v${KARPENTER_VERSION}/hack/deploy/configure-values.sh
chmod +x ./configure-values.sh && ./configure-values.sh ${CLUSTER_NAME} ${RG} karpenter-sa karpentermsi
```

Check the `karpenter-values.yaml` file was created:

```bash
ls
```

```
bin  configure-values.sh  karpenter-values-template.yaml  karpenter-values.yaml
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

Check karpenter version by using `helm list` command.

```bash
helm list -n "${KARPENTER_NAMESPACE}"
```

Expected to see `aks-managed-workload-identity` and `cilium` here as well, but if things worked correctly you should see a karpenter line like the following:

```
NAME       NAMESPACE       REVISION  UPDATED                                 STATUS    CHART
karpenter  kube-system     1         2024-11-07 19:17:08.982543921 +0000 UTC deployed  karpenter-0.7.0
```

To check the running pods, use the following command, which should return 1 pod:

```bash
kubectl get pods --namespace "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter
```

```
NAME                         READY   STATUS    RESTARTS   AGE
karpenter-5878b8bbd9-46cnm   1/1     Running   0          27s
```

You can also check the karpenter pod logs with the following:

```bash
kubectl logs -f -n "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter -c controller
```

### Create workshop namespace

Now let's create a namespace which we'll be using for all our work in this workshop moving forward:

```bash
kubectl create namespace workshop
```

### K9s

You can also try using k9s to inspect the cluster. We'll be using it throughout certain chapers of the workshop to check on the status of the pods deployed to the AKS cluster. To do so, use the command below:

```bash
k9s -n all
```

You can press `?` to learn more about the options and press `:q` to exit from `k9s`.