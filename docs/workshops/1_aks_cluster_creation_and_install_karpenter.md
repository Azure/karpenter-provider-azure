Table of contents:
- [Installation](#installation)
  - [Install utilities](#install-utilities)
  - [Create a cluster](#create-a-cluster)
  - [Configure Helm chart values](#configure-helm-chart-values)
  - [Install Karpenter](#install-karpenter)
  - [Create our workshop namespace](#create-our-workshop-namespace)

## Environment Setup

### Pre-requisite

You must have an Azure account, and personal Azure subscription.

> Note: this will use your chosen subscription for any pricing/costs associated with the workshop. At the end of the workshop, see step [Cleanup](https://github.com/Azure/karpenter-provider-azure/blob/main/docs/workshops/kubecon_azure_track.md#cleanup) to ensure all the resources are properly cleaned up to eliminate any additional costs.

### Launch the Cloud Shell Terminal

Open [https://shell.azure.com/](https://shell.azure.com/) in a new tab.

> Note: <br>
> \- If you do get disconnected from the Cloud Shell, and find your setup is not working, you can use the following document's quick and easy steps to reestablish it: [reestablish_env.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/reestablish_env.md). (this will only work if you have already completed all the steps of installation in this current doc)

### Create a Directory for the Workshop

Create the workshop's directory hierarchy, and add it's tooling bin to the path.

```bash
mkdir -p ~/environment/karpenter/bin
export PATH=$PATH:~/environment/karpenter/bin
```

### Install Utilities

Use the below command to install `yq`, `k9s`, and `aks-node-viewer` all used for this workshop:

```bash
cd ~/environment/karpenter/bin

# yq - used by some of the scripts below
wget https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -O ~/environment/karpenter/bin/yq
chmod +x ~/environment/karpenter/bin/yq

# k9s - terminal UI to interact with the Kubernetes clusters
wget https://github.com/derailed/k9s/releases/download/v0.32.5/k9s_Linux_amd64.tar.gz -O ~/environment/karpenter/bin/k9s.tar.gz
tar -xf k9s.tar.gz

# aks-node-viewer - used for tracking price, and other metrics of nodes
wget https://github.com/Azure/aks-node-viewer/releases/download/v0.0.2-alpha/aks-node-viewer_Linux_x86_64 -O ~/environment/karpenter/bin/aks-node-viewer
chmod +x ~/environment/karpenter/bin/aks-node-viewer
```

## Installation

In these next steps, we'll create an AKS cluster and install self-hosted Karpenter on it.

> Note: there is a managed version of Karpenter within AKS, called NAP (Node Autoprovisioning), with some more opinionated defaults and base scaling configurations. However, we'll be exploring the self-hosted approach today.

### Create a cluster

Create a new AKS cluster with the required configuration, and ready to run Karpenter using a workload identity.

Select the subscription to use (replace `<personal-azure-sub>` with your azure subscription guid):

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

Create the AKS cluster compatible with Karpenter, where workload identity is enabled:

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

> Note: If you've been disconnected from Cloud Shell, the env vars may have been removed. If you experience this issue follow [reestablish_env.md](https://github.com/Azure/karpenter-provider-azure/tree/main/docs/workshops/reestablish_env.md), along with restoring AKS_JSON, and KMSI_JSON using the command below. AKS_JSON, and KMSI_JSON are only required for the next two bash scripts, and not required for any future env recovery.
> ```bash
> AKS_JSON=$(az aks show --name "${CLUSTER_NAME}" --resource-group "${RG}")
> KMSI_JSON=$(az identity show --name karpentermsi --resource-group "${RG}")
> ```

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

You should see the file within the output:

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

It's expected to see `aks-managed-workload-identity` and `cilium` here as well, but if things worked correctly you should see a karpenter line like the following:

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

### Create our workshop namespace

Now let's create a namespace which we'll be using for all our work in this workshop moving forward:

```bash
kubectl create namespace workshop
```

### K9s

You can also try using k9s to inspect the cluster. We'll be using it throughout certain chapters of the workshop to check on the status of the pods deployed to the AKS cluster. To do so, use the command below:

```bash
k9s -n all
```

You can press `?` to learn more about the options and press `:q` to exit from `k9s`.
