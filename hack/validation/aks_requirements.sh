#!/bin/bash
set -euo pipefail

# AKS (kubernetes.azure.com) requirements validation for nodeclaim, nodepool, and nodeoverlay
# Restricts kubernetes.azure.com domain in requirement keys to only labels that
# Karpenter/AKS manages, syncing with AKS RP Machine API validation.

rule=$'self in
    [
        "kubernetes.azure.com/cluster",
        "kubernetes.azure.com/mode",
        "kubernetes.azure.com/scalesetpriority",
        "kubernetes.azure.com/os-sku",
        "kubernetes.azure.com/fips_enabled",
        "kubernetes.azure.com/os-sku-effective",
        "kubernetes.azure.com/os-sku-requested",
        "kubernetes.azure.com/sku-cpu",
        "kubernetes.azure.com/sku-memory"
    ]
    || !self.find("^([^/]+)").endsWith("kubernetes.azure.com")
'

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml)  -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeoverlays.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# nodeclaim
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# overlays
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml
