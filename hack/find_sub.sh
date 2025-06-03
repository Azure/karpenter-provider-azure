#!/bin/bash

SUBSCRIPTIONS=()

REGIONS=($(az account list-locations --query "[?metadata.regionType=='Physical'].name" -o tsv))

SKUS=(
  "Standard_D2_v5"
  "Standard_E4d_v5"
  )

for SUB in "${SUBSCRIPTIONS[@]}"; do
  echo -e "\n🔎 Checking Subscription: $SUB"

  for REGION in "${REGIONS[@]}"; do
    for SKU in "${SKUS[@]}"; do
      # TODO Could make parallel to speed things along      
      echo "Checking $SKU in $REGION under $SUB"
      AVAILABLE=$(az vm list-skus \
        --subscription "$SUB" \
        --location "$REGION" \
        --size "$SKU" \
        --query "[?length(restrictions)==\`0\`].name" -o tsv 2>/dev/null)

      if [[ "$AVAILABLE" == "$SKU" ]]; then
        echo "✅ $SKU is AVAILABLE in $REGION under $SUB"
      else
        echo "❌ $SKU is RESTRICTED in $REGION under $SUB"
      fi
    done
  done
done
