#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# checking for restricted taints in kubernetes.azure.com domain

# Rule for startupTaints: allow only egressgateway.kubernetes.azure.com/cni-not-ready, block all other kubernetes.azure.com domain taints
startupTaintsDomainRule='self.all(x, x.key in ["egressgateway.kubernetes.azure.com/cni-not-ready"] || !x.key.split("/")[0].endsWith("kubernetes.azure.com"))'
startupTaintsValueRule='self.all(x, !(x.key == "egressgateway.kubernetes.azure.com/cni-not-ready" && !(x.value == "true" && x.effect == "NoSchedule")))'

# Rules for taints: allow only specific kubernetes.azure.com taints with exact values/effects
taintsDomainRule='self.all(x, x.key in ["kubernetes.azure.com/scalesetpriority", "kubernetes.azure.com/mode"] || !x.key.split("/")[0].endsWith("kubernetes.azure.com"))'
scalesetpriorityRule='self.all(x, !(x.key == "kubernetes.azure.com/scalesetpriority" && !(x.value == "spot" && x.effect == "NoSchedule")))'
modeRule='self.all(x, !(x.key == "kubernetes.azure.com/mode" && !(x.value == "gateway" && x.effect == "NoSchedule")))'

# Escape quotes and clean up the rules
startupTaintsDomainRule=${startupTaintsDomainRule//\"/\\\"}
startupTaintsDomainRule=${startupTaintsDomainRule//$'\n'/}
startupTaintsDomainRule=$(echo "$startupTaintsDomainRule" | tr -s ' ')

startupTaintsValueRule=${startupTaintsValueRule//\"/\\\"}
startupTaintsValueRule=${startupTaintsValueRule//$'\n'/}
startupTaintsValueRule=$(echo "$startupTaintsValueRule" | tr -s ' ')

taintsDomainRule=${taintsDomainRule//\"/\\\"}
taintsDomainRule=${taintsDomainRule//$'\n'/}
taintsDomainRule=$(echo "$taintsDomainRule" | tr -s ' ')

scalesetpriorityRule=${scalesetpriorityRule//\"/\\\"}
scalesetpriorityRule=${scalesetpriorityRule//$'\n'/}
scalesetpriorityRule=$(echo "$scalesetpriorityRule" | tr -s ' ')

modeRule=${modeRule//\"/\\\"}
modeRule=${modeRule//$'\n'/}
modeRule=$(echo "$modeRule" | tr -s ' ')

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# Add validation for startupTaints at array level
printf -v startupTaintsDomainExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$startupTaintsDomainRule"
yq eval "${startupTaintsDomainExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v startupTaintsValueExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.x-kubernetes-validations +=
    [{"message": "taint \\"egressgateway.kubernetes.azure.com/cni-not-ready\\" must have value \\"true\\" and effect \\"NoSchedule\\"", "rule": "%s"}]' "$startupTaintsValueRule"
yq eval "${startupTaintsValueExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Add validation for taints at array level
printf -v taintsDomainExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$taintsDomainRule"
yq eval "${taintsDomainExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v scalesetpriorityExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/scalesetpriority\\" must have value \\"spot\\" and effect \\"NoSchedule\\"", "rule": "%s"}]' "$scalesetpriorityRule"
yq eval "${scalesetpriorityExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v modeExpr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/mode\\" must have value \\"gateway\\" and effect \\"NoSchedule\\"", "rule": "%s"}]' "$modeRule"
yq eval "${modeExpr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Set maxItems limit for both arrays (required for CEL validation cost budget)
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Set maxLength limit for taint keys (Kubernetes limit is 63 characters)
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.maxLength = 63' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.properties.key.maxLength = 63' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
