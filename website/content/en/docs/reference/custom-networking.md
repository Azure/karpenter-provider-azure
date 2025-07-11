---
title: "Custom Networking"
linkTitle: "Custom Networking"
weight: 6
description: >
  Configure Karpenter with custom VNet and subnet configurations
---

## Cluster Setup with Custom VNet and Subnets

Karpenter supports custom networking configurations that allow you to specify different subnets for your nodes. This is particularly useful when you need to place nodes in specific subnets for compliance, security, or network segmentation requirements.
### Creating VNet and Subnets

First, create a VNet with two subnets for your AKS cluster:

```bash
# Set variables
RESOURCE_GROUP="my-aks-rg"
LOCATION="eastus"
VNET_NAME="my-aks-vnet"
CLUSTER_SUBNET="cluster-subnet"
CUSTOM_SUBNET="custom-subnet"
CLUSTER_NAME="my-aks-cluster"

# Create resource group
az group create --name $RESOURCE_GROUP --location $LOCATION

# Create VNet with address space
az network vnet create \
  --resource-group $RESOURCE_GROUP \
  --name $VNET_NAME \
  --address-prefixes 10.0.0.0/16

# Create cluster subnet for main AKS nodes
az network vnet subnet create \
  --resource-group $RESOURCE_GROUP \
  --vnet-name $VNET_NAME \
  --name $CLUSTER_SUBNET \
  --address-prefixes 10.0.1.0/24

# Create custom subnet for Karpenter nodes
az network vnet subnet create \
  --resource-group $RESOURCE_GROUP \
  --vnet-name $VNET_NAME \
  --name $CUSTOM_SUBNET \
  --address-prefixes 10.0.2.0/24
```

### Creating AKS Cluster with Custom VNet

Create the AKS cluster using the cluster subnet:

```bash
# Get subnet ID for cluster creation
CLUSTER_SUBNET_ID=$(az network vnet subnet show \
  --resource-group $RESOURCE_GROUP \
  --vnet-name $VNET_NAME \
  --name $CLUSTER_SUBNET \
  --query id -o tsv)

# Create AKS cluster with custom VNet and Karpenter enabled
az aks create \
  --resource-group $RESOURCE_GROUP \
  --name $CLUSTER_NAME \
  --node-count 1 \
  --vnet-subnet-id $CLUSTER_SUBNET_ID \
  --network-plugin azure \
  --enable-managed-identity \
  --node-provisioning-mode Auto \
  --generate-ssh-keys
```

### Karpenter Installation

Karpenter is automatically installed when using `--node-provisioning-mode Auto` during cluster creation.

## Prerequisites

- Azure CLI installed and authenticated
- An AKS cluster with Karpenter installed (created above)
- Custom subnets in your VNet (created above)
- Appropriate RBAC permissions for subnet access

## VNet Subnet Configuration

You can configure custom subnet IDs in your AKSNodeClass using the `vnetSubnetID` field:

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: custom-networking
spec:
  vnetSubnetID: "/subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/resourceGroups/my-aks-rg/providers/Microsoft.Network/virtualNetworks/my-aks-vnet/subnets/custom-subnet"
```

## RBAC Configuration

When using custom subnet configurations, Karpenter needs appropriate permissions to read subnet information and join nodes to the specified subnets. There are two recommended approaches for configuring these permissions.

### Approach A: Broad VNet Permissions

This approach grants the cluster identity permissions to read and join any subnet within the main VNet, as well as overall network contributor. Its very permissive, investigate the "Network Contributor" role before applying this to your production cluster. 

#### Required Permissions

Assign the following roles to your cluster identity at the VNet scope:

```bash
# Get your cluster's managed identity
CLUSTER_IDENTITY=$(az aks show --resource-group <cluster-rg> --name <cluster-name> --query identity.principalId -o tsv)

# Get your VNet resource ID
VNET_ID="/subscriptions/<subscription-id>/resourceGroups/<vnet-rg>/providers/Microsoft.Network/virtualNetworks/<vnet-name>"

# Assign Network Contributor role for subnet read/join operations
az role assignment create \
  --assignee $CLUSTER_IDENTITY \
  --role "Network Contributor" \
  --scope $VNET_ID
```

#### Benefits
- Simplified permission management
- No need to update permissions when adding new subnets
- Works well for single-tenant environments
- In cases where a subscription has reached the maximium number of custom roles, this approach works

#### Example Script
For a complete example of setting up custom networking with Approach A permissions, see this [sample setup script](https://gist.github.com/Bryce-Soghigian/a4259d6224db0c55081718caa7b37268).

#### Considerations
- Broader permissions than strictly necessary
- May not meet strict security requirements

### Approach B: Scoped Subnet Permissions

This approach grants permissions on a per-subnet basis, providing more granular control over which subnets the cluster can access.

#### Required Permissions

For each subnet you want to use with Karpenter, assign the following specific permissions:

```bash
# Get your cluster's managed identity
CLUSTER_IDENTITY=$(az aks show --resource-group <cluster-rg> --name <cluster-name> --query identity.principalId -o tsv)

# For each subnet, assign specific subnet permissions
SUBNET_ID="/subscriptions/<subscription-id>/resourceGroups/<vnet-rg>/providers/Microsoft.Network/virtualNetworks/<vnet-name>/subnets/<subnet-name>"

# Create custom role definition for subnet access
cat > subnet-access-role.json << EOF
{
  "Name": "Karpenter Subnet Access",
  "IsCustom": true,
  "Description": "Allows reading subnet information and joining VMs to subnets",
  "Actions": [
    "Microsoft.Network/virtualNetworks/subnets/read",
    "Microsoft.Network/virtualNetworks/subnets/join/action"
  ],
  "NotActions": [],
  "DataActions": [],
  "NotDataActions": [],
  "AssignableScopes": [
    "/subscriptions/<subscription-id>"
  ]
}
EOF

# Create the custom role (only needed once per subscription)
az role definition create --role-definition subnet-access-role.json

# Assign the custom role to each subnet
az role assignment create \
  --assignee $CLUSTER_IDENTITY \
  --role "Karpenter Subnet Access" \
  --scope $SUBNET_ID
```

#### Alternative: Using Built-in Roles

If you prefer using built-in roles, you can assign these specific permissions individually:

```bash
# Assign Network Reader role for subnet read access
az role assignment create \
  --assignee $CLUSTER_IDENTITY \
  --role "Network Contributor" \
  --scope $SUBNET_ID \
  --condition "((!(ActionMatches{'Microsoft.Network/virtualNetworks/subnets/write'})) AND (!(ActionMatches{'Microsoft.Network/virtualNetworks/subnets/delete'})))"
```

> **Note**: The condition parameter limits the Network Contributor role to only read and join operations on subnets, excluding write and delete permissions.

#### Benefits
- Principle of least privilege
- Granular access control
- Better compliance with security policies

#### Example Script
For a complete example of setting up custom networking with Approach B permissions, see this [scoped subnet permissions script](https://gist.github.com/Bryce-Soghigian/fc3de3a796b20dbed8fe5d2ca0c85dd4).

#### Considerations
- More complex permission management
- Need to update permissions for each new subnet
- Requires coordination between network and cluster administrators

## Example AKSNodeClass Configurations

### Single Custom Subnet

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: dedicated-workload
spec:
  vnetSubnetID: "/subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/resourceGroups/my-aks-rg/providers/Microsoft.Network/virtualNetworks/my-aks-vnet/subnets/custom-subnet"
```

### Multiple NodeClasses for Different Subnets

```yaml
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: frontend-nodes
spec:
  vnetSubnetID: "/subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/resourceGroups/my-aks-rg/providers/Microsoft.Network/virtualNetworks/my-aks-vnet/subnets/custom-subnet"
---
apiVersion: karpenter.azure.com/v1beta1
kind: AKSNodeClass
metadata:
  name: backend-nodes
spec:
  vnetSubnetID: "/subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/resourceGroups/my-aks-rg/providers/Microsoft.Network/virtualNetworks/my-aks-vnet/subnets/custom-subnet2"
```

## NodePool Configuration

Create NodePools that reference your custom AKSNodeClass:

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: custom-networking-pool
spec:
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: custom-networking
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
  limits:
    cpu: 1000
  disruption:
    consolidationPolicy: WhenEmpty
    consolidateAfter: 30s
```

## Subnet Drift Behavior

Karpenter monitors subnet configuration changes and will detect drift when the `vnetSubnetID` in an AKSNodeClass is modified. Understanding this behavior is critical when managing custom networking configurations.

### ⚠️ Unsupported Configuration Path

**Modifying `vnetSubnetID` from one valid subnet to another valid subnet is NOT a supported operation.** This field is mutable solely to provide an escape hatch for correcting invalid or malformed subnet IDs during initial configuration.

### Supported Use Case: Fixing Invalid Subnet IDs

The `vnetSubnetID` field can be modified only in these scenarios:
- Correcting a malformed subnet ID that prevents node provisioning
- Fixing an invalid subnet reference that causes configuration errors
- Updating a subnet ID that points to a non-existent or inaccessible subnet

### Unsupported Use Case: Subnet Migration

**DO NOT** use this field to migrate nodes between valid subnets. This includes:
- Moving nodes from one subnet to another for network reorganization
- Changing subnet configurations for capacity or performance reasons
- Migrating between subnets as part of infrastructure changes

**Support Policy**: Microsoft will not provide support for issues arising from subnet-to-subnet migrations via `vnetSubnetID` modifications. Support tickets related to such operations will be declined.

### What Happens When You Modify vnetSubnetID

If you modify the field (even for unsupported use cases):

1. **Drift Detection**: Karpenter detects the subnet mismatch and marks nodes for replacement
2. **Node Disruption**: Existing nodes will be cordoned, drained, and terminated
3. **Potential Issues**: Network connectivity problems, workload disruptions, and unpredictable behavior
4. **No Support**: Microsoft support will not assist with issues from this configuration path

### Recommended Approach for Subnet Changes

For legitimate subnet migration needs:
1. Create a new AKSNodeClass with the desired subnet
2. Create a new NodePool referencing the new AKSNodeClass  
3. Gradually migrate workloads to the new NodePool
4. Delete the old NodePool and AKSNodeClass when migration is complete

This approach provides controlled migration with proper testing and rollback capabilities.

## Understanding AKS Cluster CIDR Ranges

When configuring custom networking with `vnetSubnetID`, customers are responsible for understanding and managing their cluster's CIDR ranges to avoid network conflicts. Unlike traditional AKS NodePools created through ARM templates, Karpenter applies custom resource definitions (CRDs) that provision nodes instantly without the extended validation that ARM provides.

### Key CIDR Considerations

**Cluster and Service CIDRs**: Your AKS cluster is configured with specific CIDR ranges for:
- **Cluster CIDR** (`--pod-cidr`): IP range for pod networking
- **Service CIDR** (`--service-cidr`): IP range for Kubernetes services  

**Custom Subnet Requirements**: When using `vnetSubnetID`, ensure your custom subnets:
- Do not overlap with cluster, service, or use any of the reserved addresses 
- Have sufficient IP addresses for expected node and pod scaling

### Identifying Your Cluster CIDRs

Use these commands to identify your cluster's network configuration:

```bash
# Get cluster network details
az aks show --resource-group <rg-name> --name <cluster-name> \
  --query "{podCidr:networkProfile.podCidr,serviceCidr:networkProfile.serviceCidr,dockerBridgeCidr:networkProfile.dockerBridgeCidr}" \
  --output table

# Get cluster subnet information
az aks show --resource-group <rg-name> --name <cluster-name> \
  --query "agentPoolProfiles[0].vnetSubnetId" \
  --output tsv
```

### Validation Differences

**ARM Template Validation**: Traditional AKS NodePools undergo comprehensive validation:
- CIDR conflict detection
- Subnet capacity verification  
- Network policy validation
- Extended provisioning time with validation checks

**Karpenter CRD Application**: Custom resources apply immediately:
- **⚠️ No automatic CIDR conflict detection**
- **⚠️ No subnet capacity pre-validation**
- **⚠️ Instant application without extended validation**
- Faster provisioning but requires pre-planning

### Customer Responsibilities

When configuring `vnetSubnetID`, you must:

1. **Verify CIDR Compatibility**: Ensure custom subnets don't conflict with existing cluster CIDRs
2. **Plan IP Capacity**: Calculate required IP addresses for expected scaling
3. **Validate Connectivity**: Test network routes and security group rules
4. **Monitor Usage**: Track subnet utilization and plan for growth
5. **Document Configuration**: Maintain records of network design decisions

### Common CIDR Conflicts

Be aware of these potential conflicts:

```bash
# Example conflict scenarios:
# Cluster Pod CIDR: 10.244.0.0/16  
# Custom Subnet:   10.244.1.0/24  ❌ CONFLICT

# Service CIDR:    10.0.0.0/16
# Custom Subnet:   10.0.10.0/24   ❌ CONFLICT

# Safe configuration:
# Cluster Pod CIDR: 10.244.0.0/16
# Service CIDR:     10.0.0.0/16  
# Custom Subnet:    10.1.0.0/24   ✅ NO CONFLICT
```

### Network Planning Best Practices

- **Document all CIDR allocations** before implementing custom networking
- **Use non-overlapping private IP ranges** (RFC 1918)
- **Plan for future expansion** with appropriately sized subnets
- **Test configurations** in non-production environments first
- **Monitor network utilization** and plan capacity accordingly
