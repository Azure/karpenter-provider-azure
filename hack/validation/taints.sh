#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# checking for restricted taints in kubernetes.azure.com domain

# Rule for startupTaints: completely block kubernetes.azure.com domain
startupTaintsRule=$'self.all(x, !x.key.find("^([^/]+)").endsWith("kubernetes.azure.com"))'

# Rule for taints: allow only specific kubernetes.azure.com keys
taintsRule=$'self.all(x, x.key.find("^([^/]+)").endsWith("kubernetes.azure.com") ?
    x.key in [
        "kubernetes.azure.com/scalesetpriority",
        "kubernetes.azure.com/mode"
    ]
    : true
)
'

# Escape quotes and clean up the rules
startupTaintsRule=${startupTaintsRule//\"/\\\"}
startupTaintsRule=${startupTaintsRule//$'\n'/}
startupTaintsRule=$(echo "$startupTaintsRule" | tr -s ' ')

taintsRule=${taintsRule//\"/\\\"}
taintsRule=${taintsRule//$'\n'/}
taintsRule=$(echo "$taintsRule" | tr -s ' ')

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# Add validation for startupTaints
printf -v startupTaintsExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$startupTaintsRule"
yq eval "${startupTaintsExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Add validation for taints
printf -v taintsExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$taintsRule"
yq eval "${taintsExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
