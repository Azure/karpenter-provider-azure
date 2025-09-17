#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# checking for restricted taints in kubernetes.azure.com domain

# Rule for startupTaints: completely block kubernetes.azure.com domain
startupTaintsRule='!self.startsWith("kubernetes.azure.com")'

# Rules for taints: exact validation at taint object level
scalesetpriorityRule='!(self.key == "kubernetes.azure.com/scalesetpriority" && !(self.value == "spot" && self.effect == "NoSchedule"))'
modeRule='!(self.key == "kubernetes.azure.com/mode" && !(self.value == "gateway" && self.effect == "NoSchedule"))'
domainRule='!(self.key.startsWith("kubernetes.azure.com") && self.key != "kubernetes.azure.com/scalesetpriority" && self.key != "kubernetes.azure.com/mode")'

# Escape quotes and clean up the rules
startupTaintsRule=${startupTaintsRule//\"/\\\"}
startupTaintsRule=${startupTaintsRule//$'\n'/}
startupTaintsRule=$(echo "$startupTaintsRule" | tr -s ' ')

scalesetpriorityRule=${scalesetpriorityRule//\"/\\\"}
scalesetpriorityRule=${scalesetpriorityRule//$'\n'/}
scalesetpriorityRule=$(echo "$scalesetpriorityRule" | tr -s ' ')

modeRule=${modeRule//\"/\\\"}
modeRule=${modeRule//$'\n'/}
modeRule=$(echo "$modeRule" | tr -s ' ')

domainRule=${domainRule//\"/\\\"}
domainRule=${domainRule//$'\n'/}
domainRule=$(echo "$domainRule" | tr -s ' ')

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# Add validation for startupTaints key field
printf -v startupTaintsExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$startupTaintsRule"
yq eval "${startupTaintsExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Add validation for taints at the taint object level (not key level)
printf -v scalesetpriorityExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/scalesetpriority\\" must have value \\"spot\\" and effect \\"NoSchedule\\"", "rule": "%s"}]' "$scalesetpriorityRule"
yq eval "${scalesetpriorityExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v modeExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/mode\\" must have value \\"gateway\\" and effect \\"NoSchedule\\"", "rule": "%s"}]' "$modeRule"
yq eval "${modeExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v domainExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$domainRule"
yq eval "${domainExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Set maxItems limit for taints array (required for CEL validation time complexity limits)
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
