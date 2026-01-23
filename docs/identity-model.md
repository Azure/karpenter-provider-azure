# Identity Model in Karpenter Azure Provider

This document explains the different identities used in the Karpenter Azure Provider and their roles in various deployment scenarios.

## Overview

The Karpenter Azure Provider interacts with multiple Azure identities depending on the deployment mode and operation being performed. Understanding which identity is used for what purpose is crucial for proper RBAC and test configuration.

## Identity Types

### ☁️ AKS Cluster Control Plane Identity (System-assigned Managed Identity)

**What it is:**
- A managed identity automatically created and managed by Azure when an AKS cluster is created
- Found at `cluster.Identity.PrincipalID` in the AKS managed cluster resource
- Visible in the AKS cluster's resource group

**What it's used for:**
- AKS control plane operations (managing the Kubernetes API server, etc.)
- Creating and managing AKS-native node pools (not Karpenter nodes)
- Accessing resources required for AKS cluster operation
- **For AKS-managed node pools with BYOK**: This identity needs Reader permission on DiskEncryptionSet
- **For managed/AKS-hosted Karpenter with BYOK**: This identity needs Reader permission on DiskEncryptionSet and performs all VM creation operations

### ⚙️ Karpenter Controller Identity (Workload Identity)

**What it is:**
- A federated workload identity that allows the Karpenter controller pod to authenticate to Azure
- Associated with a Kubernetes ServiceAccount via Azure AD Workload Identity
- The identity that the Karpenter controller uses when making Azure API calls

**What it's used for (self-hosted deployments only):**
- **Creating and deleting VMs** through Azure Compute API for OSS.
- Creating and deleting network interfaces
- Querying Azure Resource Graph
- Accessing load balancers, subnets, network security groups
- **Reading DiskEncryptionSets** when creating VMs with customer-managed encryption
- Reading instance type information, pricing data, etc.

> **Note**: In managed/AKS-hosted deployments, the AKS Cluster Control Plane Identity performs these operations instead.


### 💻 Node Identity (VM Managed Identity)

**What it is:**
- A managed identity assigned to each VM/node created by Karpenter
- Used by kubelet and other node-level components

**What it's used for:**
- Kubelet registration with AKS
- Pulling images from Azure Container Registry (if configured)
- Node-level Azure operations

### 🧪 Test User Identity (Development/Testing)

**What it is:**
- The Azure credential used when running tests (typically `az login` credentials or a service principal)
- Retrieved via `DefaultAzureCredential` in test code

**What it's used for:**
- Creating test resources (Key Vault, Keys, DiskEncryptionSets, etc.)
- Setting up RBAC permissions for tests
- Cleanup after tests
- Only relevant in test/development scenarios

## Deployment Scenarios

### Self-Hosted Controller (In-Cluster)

**Scenario**: Karpenter controller runs as a pod within the AKS cluster

**Identity Used**:
- **Karpenter Controller Identity (Workload Identity)** for all Azure operations
- This identity makes the VM creation API calls

### Managed Controller (Out-of-Cluster)

**Scenario**: Karpenter controller runs as an AKS managed service

**Identity Used**:
- **AKS Cluster Control Plane Identity** for VM creation operations
- The cluster identity makes the VM creation API calls (delegated through AKS)

### E2E Tests
**Scenario**: Running e2e tests with `InClusterController=true` or `false`

**Identities Used**:
1. **Test User Identity**: Creates test infrastructure (Azure resources, test pods, Karpenter CRs)
2. **Controlling Identity** (depends on mode):
   - `InClusterController=true`: **Karpenter Controller Identity** creates VMs
   - `InClusterController=false`: **Cluster Control Plane Identity** creates VMs

**Test Setup Pattern**:
```go
// Determine the controlling identity based on deployment mode
var controllingIdentity string
if env.InClusterController {
    // In-cluster: Karpenter workload identity creates VMs
    controllingIdentity = env.GetKarpenterWorkloadIdentity(ctx)
} else {
    // Out-of-cluster: Cluster identity creates VMs (via AKS)
    clusterIdentity := env.GetClusterIdentity(ctx)
    controllingIdentity = lo.FromPtr(clusterIdentity.PrincipalID)
}
```
