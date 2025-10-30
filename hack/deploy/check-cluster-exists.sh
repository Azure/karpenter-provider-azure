#!/bin/bash

# Script to check if AKS cluster exists with the correct make-command tag
# Usage: check-cluster-exists.sh <cluster-name> <resource-group> <expected-tag-value>
# Exit codes:
#   0 - Cluster exists with correct tag (skip creation)
#   1 - Cluster doesn't exist (proceed with creation)
#   2 - Cluster exists but has wrong/missing tag (error)

set -e

CLUSTER_NAME="$1"
RESOURCE_GROUP="$2"
EXPECTED_TAG="$3"

if [ -z "$CLUSTER_NAME" ] || [ -z "$RESOURCE_GROUP" ] || [ -z "$EXPECTED_TAG" ]; then
    echo "Usage: $0 <cluster-name> <resource-group> <expected-tag-value>"
    exit 2
fi

# Check if cluster exists
if az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" -o none 2>/dev/null; then
    # Cluster exists, check the tag
    EXISTING_TAG=$(az aks show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" --query "tags.\"make-command\"" -o tsv 2>/dev/null || echo "")

    if [ "$EXISTING_TAG" = "$EXPECTED_TAG" ]; then
        echo "Cluster $CLUSTER_NAME already exists with correct tag, skipping creation"
        exit 0  # Skip creation
    else
        echo "Error: Cluster $CLUSTER_NAME exists but does not have the required tag 'make-command: $EXPECTED_TAG'"
        echo "Current tag value: $EXISTING_TAG"
        exit 2  # Error condition
    fi
else
    # Cluster doesn't exist
    exit 1  # Proceed with creation
fi
