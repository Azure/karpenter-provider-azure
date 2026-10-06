#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <resource-group> <acr-name> <aks-cluster-name>"
    exit 1
fi

RESOURCE_GROUP_NAME=$1
ACR_NAME=$2
AKS_CLUSTER_NAME=$3

echo "Validating resource group $RESOURCE_GROUP_NAME ..."
if ! az group show --name "$RESOURCE_GROUP_NAME" &>/dev/null; then
    echo "Resource group $RESOURCE_GROUP_NAME does not exist."
    exit 1
fi

echo "Validating ACR $ACR_NAME ..."
if ! az acr show --name "$ACR_NAME" --resource-group "$RESOURCE_GROUP_NAME" &>/dev/null; then
    echo "ACR $ACR_NAME does not exist in resource group $RESOURCE_GROUP_NAME."
    exit 1
fi

echo "Validating AKS cluster $AKS_CLUSTER_NAME ..."
if ! az aks show --resource-group "$RESOURCE_GROUP_NAME" --name "$AKS_CLUSTER_NAME" &>/dev/null; then
    echo "AKS cluster $AKS_CLUSTER_NAME does not exist in resource group $RESOURCE_GROUP_NAME."
    exit 1
fi

echo "Fetching node resource group for AKS cluster $AKS_CLUSTER_NAME in resource group $RESOURCE_GROUP_NAME ..."
NODE_RESOURCE_GROUP=$(az aks show --resource-group "$RESOURCE_GROUP_NAME" --name "$AKS_CLUSTER_NAME" --query nodeResourceGroup -o tsv)

echo "Fetching ACR registry ID for ACR $ACR_NAME in resource group $RESOURCE_GROUP_NAME ..."
ACR_ID=$(az acr show --name "$ACR_NAME" --resource-group "$RESOURCE_GROUP_NAME" --query id --output tsv)

echo "Checking if AcrPull role assignment already exists ..."
ASSIGNEE=$(az aks show --resource-group "$RESOURCE_GROUP_NAME" --name "$AKS_CLUSTER_NAME" --query identityProfile.kubeletidentity.clientId -o tsv)
if az role assignment list --assignee "$ASSIGNEE" --scope "$ACR_ID" --role "AcrPull" | grep -q "$ASSIGNEE"; then
    echo "AcrPull role assignment already exists for $ASSIGNEE on $ACR_ID."
else
    echo "Assigning AcrPull role to the managed identity of the node resource group ..."
    az role assignment create --assignee "$ASSIGNEE" --role AcrPull --scope "$ACR_ID"
fi
