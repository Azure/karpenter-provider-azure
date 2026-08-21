#!/usr/bin/env bash
set -euo pipefail
# This script interrogates the AKS cluster and Azure resources to generate
# the karpenter-values.yaml file using the karpenter-values-template.yaml file as a template.

if [ "$#" -lt 5 ] || [ "$#" -gt 7 ]; then
    echo "Usage: $0 <cluster-name> <resource-group> <karpenter-service-account-name> <karpenter-user-assigned-identity-name> <enable-azure-sdk-logging> [provision-mode] [aks-machines-pool-name]"
    exit 1
fi

echo "Configuring karpenter-values.yaml for cluster $1 in resource group $2 ..."

CLUSTER_NAME=$1
AZURE_RESOURCE_GROUP=$2
KARPENTER_SERVICE_ACCOUNT_NAME=$3
AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME=$4
ENABLE_AZURE_SDK_LOGGING=$5
PROVISION_MODE=${6:-}
AKS_MACHINES_POOL_NAME=${7:-testmpool}

# Validate PROVISION_MODE
if [[ -n "$PROVISION_MODE" && "$PROVISION_MODE" != "aksmachineapi" && "$PROVISION_MODE" != "aksmachineapiheaderbatch" && "$PROVISION_MODE" != "bootstrappingclient" && "$PROVISION_MODE" != "aksscriptless" ]]; then
    echo "Error: Invalid provision-mode '$PROVISION_MODE'. Must be one of: aksmachineapi, aksmachineapiheaderbatch, bootstrappingclient, aksscriptless, or empty"
    exit 1
fi

# Optional values through env vars:
LOG_LEVEL=${LOG_LEVEL:-"info"}

AKS_JSON=$(az aks show --name "$CLUSTER_NAME" --resource-group "$AZURE_RESOURCE_GROUP" --output json)
AZURE_LOCATION=$(jq -r ".location" <<< "$AKS_JSON")
AZURE_RESOURCE_GROUP_MC=$(jq -r ".nodeResourceGroup" <<< "$AKS_JSON")
AZURE_SUBSCRIPTION_ID=$(az account show --query 'id' --output tsv)

CLUSTER_ENDPOINT=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')

TOKEN_SECRET_NAME=$(kubectl get -n kube-system secrets --field-selector=type=bootstrap.kubernetes.io/token -o jsonpath='{.items[0].metadata.name}')
TOKEN_ID=$(kubectl get -n kube-system secret "$TOKEN_SECRET_NAME" -o jsonpath='{.data.token-id}' | base64 -d)
TOKEN_SECRET=$(kubectl get -n kube-system secret "$TOKEN_SECRET_NAME" -o jsonpath='{.data.token-secret}' | base64 -d)
BOOTSTRAP_TOKEN=$TOKEN_ID.$TOKEN_SECRET

SSH_PUBLIC_KEY="$(cat ~/.ssh/id_rsa.pub) azureuser"


get_vnet_json() {
    local node_subnet_id=$1
    local resource_group=$2

    # Resolve the VNet owning the node subnet, so VNET_GUID always matches VNET_SUBNET_ID
    if [[ -n "$node_subnet_id" ]]; then
        az network vnet show --ids "${node_subnet_id%/subnets/*}" --output json
        return
    fi

    az network vnet list --resource-group "$resource_group" --output json | jq -r ".[0]"
}

# AKS reports the node subnet for BYO VNet clusters. Managed VNets leave it null and hold one subnet
VNET_SUBNET_ID=$(jq -r ".agentPoolProfiles[0].vnetSubnetId // empty" <<< "$AKS_JSON")
VNET_JSON=$(get_vnet_json "$VNET_SUBNET_ID" "$AZURE_RESOURCE_GROUP_MC")
if [[ -z "$VNET_SUBNET_ID" ]]; then
    VNET_SUBNET_ID=$(jq -r ".subnets[0].id" <<< "$VNET_JSON")
fi
# Set only on clusters using Azure CNI with pod subnet. Not supported on AKS machine API modes
POD_SUBNET_ID=""
if [[ "$PROVISION_MODE" != "aksmachineapi" && "$PROVISION_MODE" != "aksmachineapiheaderbatch" ]]; then
    POD_SUBNET_ID=$(jq -r ".agentPoolProfiles[0].podSubnetId // empty" <<< "$AKS_JSON")
fi
# Static Block pools also report podSubnetId, but need a different pod network type
POD_IP_ALLOCATION_MODE=$(jq -r ".agentPoolProfiles[0].podIpAllocationMode // empty" <<< "$AKS_JSON")
if [[ -n "$POD_SUBNET_ID" && "$POD_IP_ALLOCATION_MODE" == "StaticBlock" ]]; then
    echo "Error: pod IP allocation mode 'StaticBlock' is not supported. Only dynamic pod subnet allocation is."
    exit 1
fi
VNET_GUID=$(jq -r ".resourceGuid // empty" <<< "$VNET_JSON")

# The // empty ensures that if the files is 'null' or not present jq will output nothing
# If the value returned is none, its from jq and not the aks api in this case we return ""
NETWORK_PLUGIN=$(jq -r ".networkProfile.networkPlugin // empty | if . == \"none\" then \"\" else . end" <<< "$AKS_JSON")
NETWORK_PLUGIN_MODE=$(jq -r ".networkProfile.networkPluginMode // empty | if . == \"none\" then \"\" else . end" <<< "$AKS_JSON")
NETWORK_POLICY=$(jq -r ".networkProfile.networkPolicy // empty | if . == \"none\" then \"\" else . end" <<< "$AKS_JSON")
NETWORK_DATAPLANE=$(jq -r ".networkProfile.networkDataplane // empty | if . == \"none\" then \"\" else . end" <<< "$AKS_JSON")

NODE_IDENTITIES=$(jq -r ".identityProfile.kubeletidentity.resourceId" <<< "$AKS_JSON")

KARPENTER_USER_ASSIGNED_CLIENT_ID=$(az identity show --resource-group "${AZURE_RESOURCE_GROUP}" --name "${AZURE_KARPENTER_USER_ASSIGNED_IDENTITY_NAME}" --query 'clientId' --output tsv)
KUBELET_IDENTITY_CLIENT_ID=$(jq -r ".identityProfile.kubeletidentity.clientId // empty" <<< "$AKS_JSON")

AZURE_SIG_SUBSCRIPTION_ID=""
USE_SIG="false"
# For Machine API modes
if [[ "${PROVISION_MODE:-}" == "aksmachineapi" || "${PROVISION_MODE:-}" == "aksmachineapiheaderbatch" ]]; then
    USE_SIG="true"
    AZURE_SIG_SUBSCRIPTION_ID=109a5e88-712a-48ae-9078-9ca8b3c81345
fi

export CLUSTER_NAME AZURE_LOCATION AZURE_RESOURCE_GROUP AZURE_RESOURCE_GROUP_MC KARPENTER_SERVICE_ACCOUNT_NAME \
    CLUSTER_ENDPOINT BOOTSTRAP_TOKEN SSH_PUBLIC_KEY VNET_SUBNET_ID POD_SUBNET_ID KARPENTER_USER_ASSIGNED_CLIENT_ID NODE_IDENTITIES AZURE_SUBSCRIPTION_ID NETWORK_PLUGIN NETWORK_PLUGIN_MODE NETWORK_POLICY NETWORK_DATAPLANE \
    LOG_LEVEL VNET_GUID KUBELET_IDENTITY_CLIENT_ID ENABLE_AZURE_SDK_LOGGING PROVISION_MODE USE_SIG AZURE_SIG_SUBSCRIPTION_ID AKS_MACHINES_POOL_NAME

# get karpenter-values-template.yaml, if not already present (e.g. outside of repo context)
if [ ! -f karpenter-values-template.yaml ]; then
    curl -sO https://raw.githubusercontent.com/Azure/karpenter-provider-azure/main/karpenter-values-template.yaml
fi
yq '(.. | select(tag == "!!str")) |= envsubst(nu)' karpenter-values-template.yaml > karpenter-values.yaml
