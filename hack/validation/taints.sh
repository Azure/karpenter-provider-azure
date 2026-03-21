#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# Block taints with AKS system prefix (kubernetes.azure.com/) to prevent
# validation mismatches with AKS Machine API server-side validation.
# This ensures NodePool CRD validation stays in sync with RP validation.
# See: AKS RP nodetaintsvalidator.go — blocks kubernetes.azure.com/ prefix.

# Use startsWith on the key directly — simpler and cheaper than regex.
# Covers both "kubernetes.azure.com/foo" (domain + path) patterns.
rule=$'self.all(x, !x.key.startsWith("kubernetes.azure.com/"))'

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# nodepool taints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool startupTaints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
