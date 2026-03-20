#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# Generates CEL rules from the authoritative Go source (pkg/apis/validation/aksrules.go)
# to block kubernetes.azure.com/* taint keys unless allowlisted.
#
# Also adds maxItems to taints arrays to satisfy CEL cost estimation budget.
#
# This ensures CRD validation stays in sync with AKS RP validation rules.
# See: https://github.com/Azure/karpenter-poc/issues/1710

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Generate the CEL rule from Go constants
rule=$(go run "${REPO_ROOT}/hack/validation/cmd/gencel" -type taints)

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# Add maxItems to taints and startupTaints arrays (needed for CEL cost estimation).
# 100 is a generous upper bound; AKS RP does not impose a hard limit on taints count,
# but having >100 taints on a node is not realistic.
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.maxItems = 100' \
    -i pkg/apis/crds/karpenter.sh_nodepools.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.maxItems = 100' \
    -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool: taints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool: startupTaints
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
