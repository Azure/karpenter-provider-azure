#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# checking for restricted taints in kubernetes.azure.com domain

# Rule for startupTaints: completely block kubernetes.azure.com domain
startupTaintsRule='!self.startsWith("kubernetes.azure.com")'

# Rule for taints: allow only specific kubernetes.azure.com keys (negated logic for CEL validation)
taintsRule='!(self.startsWith("kubernetes.azure.com") && self != "kubernetes.azure.com/scalesetpriority" && self != "kubernetes.azure.com/mode")'

# Escape quotes and clean up the rules
startupTaintsRule=${startupTaintsRule//\"/\\\"}
startupTaintsRule=${startupTaintsRule//$'\n'/}
startupTaintsRule=$(echo "$startupTaintsRule" | tr -s ' ')

taintsRule=${taintsRule//\"/\\\"}
taintsRule=${taintsRule//$'\n'/}
taintsRule=$(echo "$taintsRule" | tr -s ' ')

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# Add validation for startupTaints key field
printf -v startupTaintsExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$startupTaintsRule"
yq eval "${startupTaintsExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Add validation for taints key field
printf -v taintsExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$taintsRule"
yq eval "${taintsExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Set maxItems limit for taints array (required for CEL validation time complexity limits)
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
