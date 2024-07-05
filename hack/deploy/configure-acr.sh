#!/bin/bash

# This script configures a custom Azure Container Registry (ACR) for Karpenter managed nodes.

# Variables
RESOURCE_GROUP_NAME="<your-resource-group>"
ACR_NAME="<your-acr-name>"
AKS_CLUSTER_NAME="<your-aks-cluster-name>"
NODE_RESOURCE_GROUP=$(az aks show --resource-group $RESOURCE_GROUP_NAME --name $AKS_CLUSTER_NAME --query nodeResourceGroup -o tsv)

# Get the ACR registry ID
ACR_ID=$(az acr show --name $ACR_NAME --resource-group $RESOURCE_GROUP_NAME --query id --output tsv)

# Assign the AcrPull role to the managed identity of the node resource group
az role assignment create --assignee $(az aks show --resource-group $RESOURCE_GROUP_NAME --name $AKS_CLUSTER_NAME --query identityProfile.kubeletidentity.clientId -o tsv) --role AcrPull --scope $ACR_ID

echo "Custom ACR configured for Karpenter managed nodes."
