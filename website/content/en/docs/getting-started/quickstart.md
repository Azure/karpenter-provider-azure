---
title: "Quickstart"
linkTitle: "Quickstart"
weight: 10
description: >
  Get started with Karpenter for Azure in minutes
---

This quickstart will help you deploy Karpenter for Azure on your AKS cluster. Karpenter automatically provisions right-sized nodes based on your workload requirements, improving efficiency and reducing costs.

## Choose Your Deployment Mode

Karpenter for Azure supports two deployment modes:

- **[Node Auto Provisioning (NAP)](#node-auto-provisioning-nap)** - Managed by AKS (Recommended)
- **[Self-hosted](#self-hosted-mode)** - You manage the deployment

## Node Auto Provisioning (NAP)

NAP is the recommended way to use Karpenter with AKS. Microsoft manages the entire Karpenter lifecycle as a managed addon.

### Prerequisites

- Azure subscription
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli) installed
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/) installed

### Create AKS Cluster with NAP

Set up your environment:

```bash
# Set environment variables
export CLUSTER_NAME=karpenter-nap-cluster
export RESOURCE_GROUP=karpenter-demo
export LOCATION=westus2

# Login to Azure
az login
```

Create the resource group:

```bash
az group create --name $RESOURCE_GROUP --location $LOCATION
```

Create AKS cluster with NAP enabled:

```bash
az aks create \
  --name $CLUSTER_NAME \
  --resource-group $RESOURCE_GROUP \
  --node-provisioning-mode Auto \
  --node-provisioning-default-pools Auto \
  --network-plugin azure \
  --network-plugin-mode overlay \
  --network-dataplane cilium \
  --node-count 3 \
  --generate-ssh-keys \
  --location $LOCATION
```

#### Understanding Default NodePools

The `--node-provisioning-default-pools` flag controls which default Karpenter NodePools are created:

- **`Auto`** (default): Creates two standard NodePools for immediate use
- **`None`**: No default NodePools are created - you must define your own

{{% alert title="Warning" color="warning" %}}
**Changing from Auto to None**: If you change this setting from `Auto` to `None` on an existing cluster, the default NodePools aren't deleted by default.
{{% /alert %}}

Get cluster credentials:

```bash
az aks get-credentials --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP
```

### Verify NAP Installation

Check that Karpenter is running:

```bash
# Verify NAP is enabled
az aks show --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP \
  --query "nodeProvisioningProfile.mode" -o tsv
```

### Understanding the Default NodePools

When using `--node-provisioning-default-pools Auto` or not specifying `--node-provisioning-default-pools`, NAP automatically creates two NodePools that cannot be deleted. `default` and `system-surge`. 

{{% alert title="Note" color="primary" %}}
**Default NodePools are protected**: When `--node-provisioning-default-pools` is set to `Auto`, the `default` and `system-surge` NodePools cannot be deleted and are managed by AKS. You can modify them, but not remove them.
{{% /alert %}}

### Create Custom NodePools

You can create additional NodePools for specific needs:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: general-purpose
spec:
  template:
    spec:
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
          values: ["D"]
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: default
      expireAfter: Never
  limits:
    cpu: 100
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 30s
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: default
spec:
  imageFamily: Ubuntu2204
EOF
```

### Test Autoprovisioning

Deploy a test workload:

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
```

Scale up to see Karpenter provision nodes:

```bash
kubectl scale deployment inflate --replicas 5

# Watch for new nodes
kubectl get nodes -w
```

Monitor Karpenter events:

```bash
kubectl get events -A --field-selector source=karpenter -w
```

### Clean Up NAP Cluster

```bash
kubectl delete deployment inflate
az aks delete --name $CLUSTER_NAME --resource-group $RESOURCE_GROUP --yes --no-wait
az group delete --name $RESOURCE_GROUP --yes --no-wait
```

## Self-hosted Mode

For advanced users who need more control over Karpenter configuration.

### Prerequisites

- Azure subscription
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli) installed
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/) installed
- [Helm 3.x](https://helm.sh/docs/intro/install/) installed
- [jq](https://stedolan.github.io/jq/) installed

### Create AKS Cluster

Set up environment variables:

```bash
export CLUSTER_NAME=karpenter-self-hosted
export RG=karpenter-demo
export LOCATION=westus3
export KARPENTER_NAMESPACE=kube-system
```

Create the resource group:

```bash
az group create --name ${RG} --location ${LOCATION}
```

Create managed identity for Karpenter:

```bash
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")
```

Create AKS cluster with workload identity:

```bash
AKS_JSON=$(az aks create \
  --name "${CLUSTER_NAME}" --resource-group "${RG}" \
  --node-count 3 --generate-ssh-keys \
  --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium \
  --enable-managed-identity \
  --enable-oidc-issuer --enable-workload-identity)

az aks get-credentials --name "${CLUSTER_NAME}" --resource-group "${RG}" --overwrite-existing
```

### Configure Workload Identity

Create federated credential:

```bash
az identity federated-credential create --name KARPENTER_FID --identity-name karpentermsi --resource-group "${RG}" \
  --issuer "$(jq -r ".oidcIssuerProfile.issuerUrl" <<< "$AKS_JSON")" \
  --subject system:serviceaccount:${KARPENTER_NAMESPACE}:karpenter-sa \
  --audience api://AzureADTokenExchange
```

Assign Azure permissions:

```bash
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(jq -r '.principalId' <<< "$KMSI_JSON")
RG_MC=$(jq -r ".nodeResourceGroup" <<< "$AKS_JSON")
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)

for role in "Virtual Machine Contributor" "Network Contributor" "Managed Identity Operator"; do
  az role assignment create --assignee "${KARPENTER_USER_ASSIGNED_CLIENT_ID}" --scope "${RG_MC_RES}" --role "$role"
done
```

### Install Karpenter

Download configuration template and script:

```bash
export KARPENTER_VERSION=0.7.0

# Download template
curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v${KARPENTER_VERSION}/hack/deploy/karpenter-values-template.yaml

# Download and run configuration script
curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/v${KARPENTER_VERSION}/hack/deploy/configure-values.sh
chmod +x ./configure-values.sh && ./configure-values.sh ${CLUSTER_NAME} ${RG} karpenter-sa karpentermsi
```

Install Karpenter using Helm:

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

### Verify Installation

Check Karpenter status:

```bash
kubectl get pods --namespace "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter
kubectl logs -f -n "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter -c controller
```

### Create NodePool

```bash
cat <<EOF | kubectl apply -f -
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: general-purpose
spec:
  template:
    spec:
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
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: default
      expireAfter: Never
  limits:
    cpu: 100
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 30s
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: default
spec:
  imageFamily: Ubuntu2204
EOF
```

### Test Autoprovisioning

Deploy and scale test workload:

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

### Clean Up Self-hosted

```bash
kubectl delete deployment inflate
helm uninstall karpenter --namespace "${KARPENTER_NAMESPACE}"
az aks delete --name "${CLUSTER_NAME}" --resource-group "${RG}" --yes --no-wait
az group delete --name "${RG}" --yes --no-wait
```

## What's Next?

- **Learn concepts**: Understand [NodePools](../concepts/nodepools/), [AKSNodeClasses](../concepts/nodeclasses/), and [disruption policies](../concepts/disruption/)
- **Advanced configuration**: Explore [GPU workloads](../tasks/managing-vm-images/#gpu-images), [spot instances](../concepts/nodepools/#spot-instances), and [custom images](../tasks/managing-vm-images/)
- **Production deployment**: Review [best practices](../best-practices/), [monitoring](../observability/), and [troubleshooting](../troubleshooting/)
- **Choose deployment mode**: Read [NAP vs Self-hosted](../concepts/deployment-modes/) comparison to understand which mode fits your needs

## Common Next Steps

### Enable Spot Instances

Add spot instance support to reduce costs:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: spot-nodepool
spec:
  template:
    spec:
      requirements:
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot", "on-demand"]  # Prefer spot, fallback to on-demand
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "E", "F"]
      nodeClassRef:
        name: default
  disruption:
    consolidationPolicy: WhenUnderutilized
```

### Add GPU Support

Configure GPU workloads:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: gpu-nodepool
spec:
  template:
    spec:
      requirements:
        - key: karpenter.azure.com/sku-gpu-count
          operator: Gt
          values: ["0"]
        - key: karpenter.azure.com/sku-gpu-manufacturer
          operator: In
          values: ["nvidia"]
      nodeClassRef:
        name: gpu-nodeclass
```

### Multi-Architecture Support

Support both AMD64 and ARM64:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: multi-arch
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64", "arm64"]
        - key: karpenter.azure.com/sku-family
          operator: In
          values: ["D", "Dpds"]  # Dpds for ARM64
```

## Getting Help

- **Documentation**: Browse the complete [documentation](../)
- **GitHub Issues**: Report bugs or request features on [GitHub](https://github.com/Azure/karpenter-provider-azure/issues)
- **Community**: Join the [#karpenter](https://kubernetes.slack.com/archives/C02SFFZSA2K) channel in Kubernetes Slack
- **Examples**: Explore more examples in the [repository](https://github.com/Azure/karpenter-provider-azure/tree/main/examples)
