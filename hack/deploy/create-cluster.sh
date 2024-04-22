#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <cluster-name> <resource-group> <namespace>"
    exit 1
fi

CLUSTER_NAME=$1
RG=$2
SYSTEM_NAMESPACE=$3

echo "Creating the workload MSI for Karpenter use ..."
LOCATION=$(az group show --name "${RG}" --query "location" -otsv)
KMSI_JSON=$(az identity create --name karpentermsi --resource-group "${RG}" --location "${LOCATION}")

echo "Creating AKS cluster $CLUSTER_NAME in resource group $RG ..."
AKS_JSON=$(az aks create \
  --name "${CLUSTER_NAME}" --resource-group "${RG}" \
  --node-count 3 --generate-ssh-keys \
  --network-plugin azure --network-plugin-mode overlay --network-dataplane cilium \
  --enable-managed-identity \
  --enable-oidc-issuer --enable-workload-identity)
az aks get-credentials --name "${CLUSTER_NAME}" --resource-group "${RG}" --overwrite-existing

echo "Creating federated credential linked to the Karpenter service account ..."
az identity federated-credential create --name KARPENTER_FID --identity-name karpentermsi --resource-group "${RG}" \
  --issuer "$(jq -r ".oidcIssuerProfile.issuerUrl" <<< "$AKS_JSON")" \
  --subject system:serviceaccount:${SYSTEM_NAMESPACE}:karpenter-sa \
  --audience api://AzureADTokenExchange

echo "Creating role assignments to let Karpenter manage VMs and Network resources ..."
KARPENTER_USER_ASSIGNED_CLIENT_ID=$(jq -r '.principalId' <<< "$KMSI_JSON")
RG_MC=$(jq -r ".nodeResourceGroup" <<< "$AKS_JSON")
RG_MC_RES=$(az group show --name "${RG_MC}" --query "id" -otsv)
for role in "Virtual Machine Contributor" "Network Contributor" "Managed Identity Operator"; do
  az role assignment create --assignee "${KARPENTER_USER_ASSIGNED_CLIENT_ID}" --scope "${RG_MC_RES}" --role "$role"
done
