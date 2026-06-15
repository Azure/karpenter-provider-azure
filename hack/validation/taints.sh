#!/bin/bash
set -euo pipefail

# taints validation for nodeclaim and nodepool
# checking for restricted taint keys while filtering out well known taints

rule=$'!self.startsWith("kubernetes.azure.com/") || self == "kubernetes.azure.com/scalesetpriority"
'
# above regex: everything before the first '/' (any characters except '/' at the beginning of the string)

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml)  -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# Add maxLength to taint key fields (required for CEL cost estimation with string operations)
# Using 316 to match the maxLength used for requirement keys (253 prefix + '/' + 63 name)
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.startupTaints.items.properties.key.maxLength = 316' -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.taints.items.properties.key.maxLength = 316' -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.maxLength = 316' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.properties.key.maxLength = 316' -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Add maxItems to taint arrays (required for CEL cost estimation to bound per-item rule cost)
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.startupTaints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.taints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.maxItems = 100' -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodeclaim - startupTaints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodeclaim - taints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool - startupTaints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool - taints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# Value validation: kubernetes.azure.com/scalesetpriority taint must have value "spot"
value_rule='self.key != \"kubernetes.azure.com/scalesetpriority\" || self.value == \"spot\"'

# nodeclaim - startupTaints (item-level)
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.startupTaints.items.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/scalesetpriority\\" must have value \\"spot\\"", "rule": "%s"}]' "$value_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodeclaim - taints (item-level)
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.taints.items.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/scalesetpriority\\" must have value \\"spot\\"", "rule": "%s"}]' "$value_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool - startupTaints (item-level)
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/scalesetpriority\\" must have value \\"spot\\"", "rule": "%s"}]' "$value_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool - taints (item-level)
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.x-kubernetes-validations +=
    [{"message": "taint \\"kubernetes.azure.com/scalesetpriority\\" must have value \\"spot\\"", "rule": "%s"}]' "$value_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
