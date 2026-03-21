#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# Block taints with AKS system prefix (kubernetes.azure.com/) to prevent
# validation mismatches with AKS Machine API server-side validation.
# This ensures NodePool CRD validation stays in sync with RP validation.
# See: AKS RP nodetaintsvalidator.go — blocks kubernetes.azure.com/ prefix.
#
# Note: CEL startsWith is case-sensitive, but this is safe because the CRD's
# taint key pattern (^([a-z0-9]...) already enforces lowercase keys.

# Use startsWith on the key directly — simpler and cheaper than regex.
rule=$'self.all(x, !x.key.startsWith("kubernetes.azure.com/"))'

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

CRD=pkg/apis/crds/karpenter.sh_nodepools.yaml

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' "$CRD") -eq 1 ]] || { echo "expected one version"; exit 1; }

# verify target paths exist in CRD (fail fast if upstream restructured the CRD)
taints_path='.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints'
startup_path='.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints'
[[ $(yq e "${taints_path} | length" "$CRD") -gt 0 ]] || { echo "ERROR: taints field not found in CRD — upstream may have restructured"; exit 1; }
[[ $(yq e "${startup_path} | length" "$CRD") -gt 0 ]] || { echo "ERROR: startupTaints field not found in CRD — upstream may have restructured"; exit 1; }

# nodepool taints
printf -v expr '%s.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$taints_path" "$rule"
yq eval "${expr}" -i "$CRD"

# nodepool startupTaints
printf -v expr '%s.x-kubernetes-validations +=
    [{"message": "taint domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$startup_path" "$rule"
yq eval "${expr}" -i "$CRD"
