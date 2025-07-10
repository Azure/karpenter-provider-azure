---
title: "Deployment Modes"
linkTitle: "Deployment Modes" 
weight: 50
description: >
  Understanding Node Auto Provisioning (NAP) vs Self-hosted deployment modes
---

Karpenter for Azure offers two distinct deployment modes to accommodate different user needs and preferences. Understanding these modes will help you choose the right approach for your AKS cluster.

## Overview

Karpenter provider for AKS can be deployed in two modes:

- **Node Auto Provisioning (NAP)**: A managed service where AKS runs Karpenter as an addon
- **Self-hosted**: A standalone deployment where you manage Karpenter directly in your cluster

## Node Auto Provisioning (NAP) Mode

### What is NAP?

[Node Auto Provisioning (NAP)](https://learn.microsoft.com/en-gb/azure/aks/node-autoprovision?tabs=azure-cli) is a managed service where AKS automatically deploys, configures, and manages Karpenter on your cluster. This is the **recommended mode for most users**.

NAP uses pending pod resource requirements to decide the optimal virtual machine configuration to run workloads in the most efficient and cost-effective manner. It automatically manages the entire Karpenter lifecycle as a managed AKS addon.

### Benefits of NAP

- **Fully managed**: Microsoft manages the Karpenter deployment, updates, and lifecycle
- **Zero operational overhead**: No need to install, configure, or maintain Karpenter
- **Automatic updates**: Karpenter is automatically updated with AKS cluster updates
- **Enterprise support**: Covered under Azure support agreements
- **Integrated monitoring**: Built-in integration with Azure Monitor and logging
- **Security**: Managed identity and RBAC configured automatically
- **Cost optimization**: Automatically provisions optimal VM configurations for workloads

### When to Use NAP

Choose NAP when:
- You want a production-ready solution with minimal operational overhead
- You prefer managed services over self-managed deployments
- You need enterprise support and SLA coverage
- You want automatic updates and security patches
- You don't need deep customization of Karpenter configuration
- You want to optimize costs automatically

### NAP Limitations

**Networking Requirements:**
The recommended network configurations for NAP are:
- Azure CNI Overlay with Powered by Cilium (recommended)
- Azure CNI Overlay
- Azure CNI with Powered by Cilium
- Azure CNI

**Unsupported features:**
- Windows node pools
- Custom kubelet configuration
- IPv6 clusters
- Service Principals (use managed identity instead)
- Disk encryption sets
- CustomCATrustCertificates
- Start/Stop mode
- HTTP proxy
- OutboundType mutation after creation
- Private cluster with custom private DNS
- Calico network policy
- Dynamic IP Allocation
- Static Allocation of CIDR blocks

### Using NAP

#### Create a new cluster with NAP

```bash
# Set environment variables
export CLUSTER_NAME=myCluster
export RESOURCE_GROUP_NAME=myResourceGroup

# Create cluster with NAP enabled
az aks create \
  --name $CLUSTER_NAME \
  --resource-group $RESOURCE_GROUP_NAME \
  --node-provisioning-mode Auto \
  --network-plugin azure \
  --network-plugin-mode overlay \
  --network-dataplane cilium \
  --generate-ssh-keys
```

#### Enable NAP on existing cluster

```bash
# Enable NAP on existing cluster
az aks update \
  --name $CLUSTER_NAME \
  --resource-group $RESOURCE_GROUP_NAME \
  --node-provisioning-mode Auto \
  --network-plugin azure \
  --network-plugin-mode overlay \
  --network-dataplane cilium
```

#### ARM Template Example

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "resources": [
    {
      "type": "Microsoft.ContainerService/managedClusters",
      "apiVersion": "2023-09-02-preview",
      "name": "napcluster",
      "location": "westus2",
      "identity": {
        "type": "SystemAssigned"
      },
      "properties": {
        "networkProfile": {
          "networkPlugin": "azure",
          "networkPluginMode": "overlay",
          "networkPolicy": "cilium",
          "networkDataplane": "cilium",
          "loadBalancerSku": "Standard"
        },
        "dnsPrefix": "napcluster",
        "agentPoolProfiles": [
          {
            "name": "agentpool",
            "count": 3,
            "vmSize": "standard_d2s_v3",
            "osType": "Linux",
            "mode": "System"
          }
        ],
        "nodeProvisioningProfile": {
          "mode": "Auto"
        }
      }
    }
  ]
}
```

## Self-hosted Mode

### What is Self-hosted Mode?

Self-hosted mode runs Karpenter as a regular Kubernetes deployment in your cluster, giving you full control over its configuration and lifecycle.

### Benefits of Self-hosted Mode

- **Full control**: Complete control over Karpenter configuration and behavior
- **Latest features**: Access to the newest Karpenter features immediately
- **Custom configuration**: Ability to customize all aspects of the deployment
- **Development and testing**: Ideal for experimentation and development
- **Multi-cloud compatibility**: Uses the same patterns as other Karpenter providers

### When to Use Self-hosted Mode

Choose self-hosted mode when:
- You need advanced customization not available in NAP
- You want to use the latest Karpenter features immediately
- You're experimenting with Karpenter or developing custom functionality
- You prefer to manage infrastructure components directly
- You need specific configuration for compliance or security requirements

### Self-hosted Limitations

- **Operational overhead**: You manage installation, updates, and maintenance
- **Support responsibility**: You're responsible for troubleshooting and support
- **Security management**: You must configure identity, RBAC, and security settings
- **Update management**: Manual process to update Karpenter versions

### Self-hosted Architecture

In self-hosted mode:

```
┌─────────────────────┐    ┌──────────────────────┐
│   AKS Cluster       │    │     Azure APIs       │
│                     │    │                      │
│  ┌───────────────┐  │    │  ┌─────────────────┐ │
│  │   Karpenter   │  │◄───┤  │  Compute API    │ │
│  │   Controller  │  │    │  │  Network API    │ │
│  │               │  │    │  │  Identity API   │ │
│  └───────────────┘  │    │  └─────────────────┘ │
│          │          │    └──────────────────────┘
│          ▼          │
│  ┌───────────────┐  │    
│  │     Nodes     │  │    
│  │  (Provisioned │  │    
│  │   by Karpenter│  │    
│  └───────────────┘  │    
└─────────────────────┘    
```

### Installing Self-hosted Karpenter

#### Prerequisites

- Azure CLI
- kubectl
- Helm 3.x
- An AKS cluster with workload identity enabled

#### Installation Steps

1. **Set up environment variables**:
```bash
export CLUSTER_NAME=myCluster
export RG=myResourceGroup
export LOCATION=westus2
export KARPENTER_NAMESPACE=kube-system
```

2. **Create managed identity**:
```bash
az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}"
```

3. **Configure workload identity**:
```bash
# Get cluster OIDC issuer
OIDC_ISSUER=$(az aks show --name "${CLUSTER_NAME}" --resource-group "${RG}" --query "oidcIssuerProfile.issuerUrl" -o tsv)

# Create federated credential
az identity federated-credential create \
  --name KARPENTER_FID \
  --identity-name karpentermsi \
  --resource-group "${RG}" \
  --issuer "${OIDC_ISSUER}" \
  --subject system:serviceaccount:${KARPENTER_NAMESPACE}:karpenter-sa \
  --audience api://AzureADTokenExchange
```

4. **Assign Azure permissions**:
```bash
# Get the node resource group
NODE_RG=$(az aks show --name "${CLUSTER_NAME}" --resource-group "${RG}" --query "nodeResourceGroup" -o tsv)

# Get MSI principal ID
MSI_PRINCIPAL_ID=$(az identity show --name karpentermsi --resource-group "${RG}" --query "principalId" -o tsv)

# Assign required roles
for role in "Virtual Machine Contributor" "Network Contributor" "Managed Identity Operator"; do
  az role assignment create \
    --assignee "${MSI_PRINCIPAL_ID}" \
    --scope "/subscriptions/$(az account show --query id -o tsv)/resourceGroups/${NODE_RG}" \
    --role "$role"
done
```

5. **Configure Helm values**:
```bash
# Download configuration script
curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/main/hack/deploy/configure-values.sh
chmod +x ./configure-values.sh

# Generate values file
./configure-values.sh ${CLUSTER_NAME} ${RG} karpenter-sa karpentermsi
```

6. **Install Karpenter**:
```bash
export KARPENTER_VERSION=0.7.0

helm upgrade --install karpenter oci://mcr.microsoft.com/aks/karpenter/karpenter \
  --version "${KARPENTER_VERSION}" \
  --namespace "${KARPENTER_NAMESPACE}" --create-namespace \
  --values karpenter-values.yaml \
  --wait
```

## Comparison Matrix

| Feature | NAP Mode | Self-hosted Mode |
|---------|----------|------------------|
| **Management** | Fully managed by Microsoft | User managed |
| **Installation** | Single CLI command | Multi-step setup process |
| **Updates** | Automatic | Manual |
| **Customization** | Limited to AKS-exposed options | Full control |
| **Support** | Azure enterprise support | Community + self-support |
| **Latest features** | Follows AKS release cycle | Immediate access |
| **Operational overhead** | Minimal | High |
| **Production readiness** | Enterprise ready | Requires operational expertise |
| **Cost** | Included in AKS | Self-managed infrastructure |
| **Security** | Auto-configured | Manual configuration required |

## Migration Between Modes

### From Self-hosted to NAP

1. **Backup current configuration**:
```bash
kubectl get nodepools,aksnodeclasses -o yaml > karpenter-backup.yaml
```

2. **Uninstall self-hosted Karpenter**:
```bash
helm uninstall karpenter -n kube-system
```

3. **Enable NAP**:
```bash
az aks update --resource-group myRG --name myCluster --enable-node-autoprovision
```

4. **Recreate NodePools** (configuration may need adjustment for NAP compatibility)

### From NAP to Self-hosted

1. **Backup NAP configuration**:
```bash
kubectl get nodepools,aksnodeclasses -o yaml > nap-backup.yaml
```

2. **Disable NAP**:
```bash
az aks update --resource-group myRG --name myCluster --disable-node-autoprovision
```

3. **Install self-hosted Karpenter** following the installation steps above

4. **Restore NodePools** (may require configuration adjustments)

## Best Practices

### For NAP Mode
- Use NAP for production workloads requiring enterprise support
- Monitor NAP feature availability in your region
- Plan capacity and scaling policies within NAP constraints
- Leverage Azure Monitor integration for observability

### For Self-hosted Mode
- Implement proper GitOps workflows for configuration management
- Set up monitoring and alerting for the Karpenter controller
- Plan regular update cycles for security and features
- Test configuration changes in non-production environments first
- Document your customizations for team knowledge sharing

## Choosing the Right Mode

**Choose NAP if:**
- You want minimal operational overhead
- You need enterprise support and SLAs
- You prefer managed services
- Your requirements fit within NAP's capabilities

**Choose Self-hosted if:**
- You need advanced customization
- You want immediate access to new features
- You're comfortable managing Kubernetes deployments
- You have specific compliance or security requirements

Both modes provide the same core Karpenter functionality for node provisioning and lifecycle management. The choice depends on your operational preferences, customization needs, and support requirements.