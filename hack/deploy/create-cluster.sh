#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <cluster-name> <resource-group>"
    exit 1
fi

CLUSTER_NAME=$1
RG=$2

# create the workload MSI that is the backing for the karpenter pod auth
LOCATION=$(az group show --name "${RG}" --query "location" -otsv)
az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}"

# create the AKS cluster compatible with Karpenter, and with the workload identity enabled
echo "Creating AKS cluster $CLUSTER_NAME in resource group $RG ..."
az aks create \
  --name "${CLUSTER_NAME}" --resource-group "${RG}" \
  --node-count 3 --generate-ssh-keys \
  --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium \
  --enable-managed-identity \
  --enable-oidc-issuer --enable-workload-identity \
  -o none
az aks get-credentials --name "${CLUSTER_NAME}" --resource-group "${RG}" --overwrite-existing

# create federated credential linked to the karpenter service account for auth usage 
AKS_OIDC_ISSUER=$(az aks show -n "${CLUSTER_NAME}" -g "${RG}" --query "oidcIssuerProfile.issuerUrl" -otsv)
az identity federated-credential create --name KARPENTER_FID --identity-name karpentermsi --resource-group "${RG}" \
  --issuer "${AKS_OIDC_ISSUER}" \
  --subject system:serviceaccount:karpenter:karpenter-sa \
  --audience api://AzureADTokenExchange

# create role assignments to let Karpenter manage VMs and Network resources
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(az identity show --resource-group "${RG}" --name karpentermsi --query 'principalId' -otsv)
RG_MC=$(az aks show --name "$CLUSTER_NAME" --resource-group "$RG" | jq -r ".nodeResourceGroup")
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)
for role in "Virtual Machine Contributor" "Network Contributor" "Managed Identity Operator"; do
  az role assignment create --assignee "${KARPENTER_USER_ASSIGNED_CLIENT_ID}" --scope "${RG_MC_RES}" --role "$role"
done
