#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 4 ]; then
    echo "Usage: $0 <subscription-id> <resource-group> <cluster-name> <nodepool-name>"
    echo "This script adds a nodepool with 'machines' mode to an existing AKS cluster using Azure REST API."
    echo "Example:"
    echo "  $0 00000000-0000-0000-0000-000000000000 some-rg some-cluster somepool"
    exit 1
fi

AZURE_SUBSCRIPTION_ID=$1
RESOURCE_GROUP=$2
CLUSTER_NAME=$3
NODEPOOL_NAME=$4

echo "Adding nodepool '$NODEPOOL_NAME' with machines mode to cluster '$CLUSTER_NAME' in resource group '$RESOURCE_GROUP'..."

# REST API endpoint
# TODO: switch to Azure CLI when it supports AgentPool creation with machines mode
API_VERSION="2025-07-01"
URL="https://management.azure.com/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${RESOURCE_GROUP}/providers/Microsoft.ContainerService/managedClusters/${CLUSTER_NAME}/agentPools/${NODEPOOL_NAME}?api-version=${API_VERSION}"

# Request body with machines mode
REQUEST_BODY=$(cat <<EOF
{
  "properties": {
    "mode": "Machines",
  }
}
EOF
)

echo "Making REST API call..."
echo "URL: $URL"
echo "Request Body:"
echo "$REQUEST_BODY"

if az rest --method PUT --url "$URL" --body "$REQUEST_BODY"; then
    echo "✅ Successfully added nodepool '$NODEPOOL_NAME' with machines mode"
else
    echo "❌ Failed to add nodepool"
    exit 1
fi
