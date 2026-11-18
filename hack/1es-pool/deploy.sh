#!/usr/bin/env bash

set -euo pipefail # fail on...failures
set -x # log commands as they run

# Note: You must run az login before executing this script

[[ -z "${AZURE_SUBSCRIPTION_ID}" ]] && echo "AZURE_SUBSCRIPTION_ID is not set" && exit 1
[[ -z "${E2E_SUBSCRIPTION_ID}" ]] && echo "E2E_SUBSCRIPTION_ID is not set (subscription where E2E tests create AKS clusters)" && exit 1

ROOT=$(dirname "${BASH_SOURCE[0]}")

RESOURCE_GROUP="karpenter-infra"
TIMESTAMP="$(date -Iseconds | tr -d :+-)"

az deployment group create -n "deployment-pool-${TIMESTAMP}" -g "${RESOURCE_GROUP}" -f "${ROOT}/deploy.bicep" \
  -p e2eSubscriptionId="${E2E_SUBSCRIPTION_ID}"
