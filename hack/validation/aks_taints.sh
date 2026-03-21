#!/bin/bash
set -euo pipefail

# AKS (kubernetes.azure.com) taints validation for nodepool and nodeclaim
# Restricts kubernetes.azure.com taint key domain to only system taints that
# AKS RP Machine API allows, syncing with AKS RP taint validation.

# Allowed AKS system taint keys (from RP's isTaintAnAllowedAKSSystemTaint):
#   - kubernetes.azure.com/scalesetpriority (spot pool taint)
#   - kubernetes.azure.com/mode (gateway pool taint)

rule=$'self in
    [
        "kubernetes.azure.com/scalesetpriority",
        "kubernetes.azure.com/mode"
    ]
    || !self.find("^([^/]+)").endsWith("kubernetes.azure.com")
'

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# nodepool - taints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool - startupTaints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodeclaim - taints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodeclaim - startupTaints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
