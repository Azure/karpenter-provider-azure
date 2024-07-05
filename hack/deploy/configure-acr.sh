#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <resource-group> <acr-name> <aks-cluster-name>"
    exit 1
fi

RESOURCE_GROUP_NAME=$1
ACR_NAME=$2
AKS_CLUSTER_NAME=$3

echo "Fetching node resource group for AKS cluster $AKS_CLUSTER_NAME in resource group $RESOURCE_GROUP_NAME ..."
NODE_RESOURCE_GROUP=$(az aks show --resource-group "$RESOURCE_GROUP_NAME" --name "$AKS_CLUSTER_NAME" --query nodeResourceGroup -o tsv)

echo "Fetching ACR registry ID for ACR $ACR_NAME in resource group $RESOURCE_GROUP_NAME ..."
ACR_ID=$(az acr show --name "$ACR_NAME" --resource-group "$RESOURCE_GROUP_NAME" --query id --output tsv)

echo "Assigning AcrPull role to the managed identity of the node resource group ..."
az role assignment create --assignee "$(az aks show --resource-group "$RESOURCE_GROUP_NAME" --name "$AKS_CLUSTER_NAME" --query identityProfile.kubeletidentity.clientId -o tsv)" --role AcrPull --scope "$ACR_ID"
