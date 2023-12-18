#!/usr/bin/env bash
set -euo pipefail

if [ -z ${ENABLE_GIT_PUSH+x} ];then
  ENABLE_GIT_PUSH=false
fi

echo "api-code-gen running ENABLE_GIT_PUSH: ${ENABLE_GIT_PUSH}"

pricing() {
  GENERATED_FILE="pkg/providers/pricing/zz_generated.pricing.go"
  NO_UPDATE=$' pkg/providers/pricing/zz_generated.pricing.go | 4 ++--\n 1 file changed, 2 insertions(+), 2 deletions(-)'
  SUBJECT="Pricing"

  go run hack/code/prices_gen.go -- "${GENERATED_FILE}"

  GIT_DIFF=$(git diff --stat "${GENERATED_FILE}")
  checkForUpdates "${GIT_DIFF}" "${NO_UPDATE}" "${SUBJECT} beside timestamps since last update" "${GENERATED_FILE}"
}

skugen() {
  location=${1:-eastus}

  # minimal defensive check to ensure the subscription is not too restrictive
  if [[ -z $(az vm list-skus --subscription "$SUBSCRIPTION_ID" --location "$location" --size Standard_D2_v5 --query "[?length(restrictions)==\`0\`]" -o tsv) ]];
  then
    echo "Please use a different subscription or region: Standard_D2_v5 has restrictions in the region $location for the subscription $SUBSCRIPTION_ID"
    exit 1
  fi

  GENERATED_FILE=$(pwd)/"pkg/fake/zz_generated.sku.$location.go"
  NO_UPDATE=" pkg/fake/zz_generated.sku.$location.go | 2 +- 1 file changed, 1 insertion(+), 1 deletion(-)"
  SUBJECT="SKUGEN"

  go run hack/code/instancetype_testdata_gen.go -- "${GENERATED_FILE}" "$location" "Standard_B1s,Standard_A0,Standard_D2_v2,Standard_D2_v3,Standard_DS2_v2,Standard_D2s_v3,Standard_D2_v5,Standard_F16s_v2,Standard_NC6s,Standard_NC6s_v3,Standard_NC16as_T4_v3,Standard_NC24ads_A100_v4,Standard_M8-2ms,Standard_D4s_v3,Standard_D64s_v3,Standard_DC8s_v3"
  go fmt "${GENERATED_FILE}"

  GIT_DIFF=$(git diff --stat "${GENERATED_FILE}")
  checkForUpdates "${GIT_DIFF}" "${NO_UPDATE}" "${SUBJECT} beside timestamps since last update" "${GENERATED_FILE}"
}


skugen-all() {
  SUBSCRIPTION_ID=$(az account show --query 'id' --output tsv)
  export SUBSCRIPTION_ID
  if [ -z "${SUBSCRIPTION_ID}" ]; then
    echo "No subscription is set. Please login and set a subscription."
    exit 1
  fi

  # run skugen for selected regions
  skugen eastus
  skugen westcentralus # non-zonal region
}


checkForUpdates() {
  GIT_DIFF=$1
  NO_UPDATE=$2
  SUBJECT=$3
  GENERATED_FILE=$4

  echo "Checking git diff for updates. ${GIT_DIFF}, ${NO_UPDATE}"
  if [[ "${GIT_DIFF}" == "${NO_UPDATE}" ]]; then
    noUpdates "${SUBJECT}"
    git checkout "${GENERATED_FILE}"
  else
    echo "true" >/tmp/api-code-gen-updates
    git add "${GENERATED_FILE}"
    if [[ $ENABLE_GIT_PUSH == true ]]; then
      gitCommitAndPush "${SUBJECT}"
    fi
  fi
}

gitOpenAndPullBranch() {
  git fetch origin
  git checkout api-code-gen || git checkout -b api-code-gen || true
}

gitCommitAndPush() {
  UPDATE_SUBJECT=$1
  git commit -m "APICodeGen updates from Azure API for ${UPDATE_SUBJECT}"
  git push --set-upstream origin api-code-gen
}

noUpdates() {
  UPDATE_SUBJECT=$1
  echo "No updates from Azure API for ${UPDATE_SUBJECT}"
}

if [[ $ENABLE_GIT_PUSH == true ]]; then
  gitOpenAndPullBranch
fi

# Run all the codegen scripts
pricing
skugen-all
