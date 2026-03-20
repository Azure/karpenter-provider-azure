#!/bin/bash
set -euo pipefail

# requirements validation for nodeclaim, nodepool, and overlays
# Adds two CEL rules to restrict requirement key domains:
# 1. karpenter.azure.com — Karpenter provider-specific labels (well-known SKU labels, etc.)
# 2. kubernetes.azure.com — AKS system labels (generated from pkg/apis/validation/aksrules.go)
#
# Rule (1) uses a hardcoded allowlist of karpenter.azure.com well-known labels.
# Rule (2) is generated from the authoritative Go source to stay in sync with AKS RP validation.
# See: https://github.com/Azure/karpenter-poc/issues/1710

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml)  -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeoverlays.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# --- Rule 1: karpenter.azure.com domain restriction (existing, provider-specific) ---
karpenter_rule=$'self in
    [
        "karpenter.azure.com/aksnodeclass",
        "karpenter.azure.com/sku-name",
        "karpenter.azure.com/sku-family",
        "karpenter.azure.com/sku-series",
        "karpenter.azure.com/sku-version",
        "karpenter.azure.com/sku-cpu",
        "karpenter.azure.com/sku-memory",
        "karpenter.azure.com/sku-networking-accelerated",
        "karpenter.azure.com/sku-storage-premium-capable",
        "karpenter.azure.com/sku-storage-ephemeralos-maxsize",
        "karpenter.azure.com/sku-gpu-name",
        "karpenter.azure.com/sku-gpu-manufacturer",
        "karpenter.azure.com/sku-gpu-count"
    ]
    || !self.find("^([^/]+)").endsWith("karpenter.azure.com")
'
karpenter_rule=${karpenter_rule//\"/\\\"}
karpenter_rule=${karpenter_rule//$'\n'/}
karpenter_rule=$(echo "$karpenter_rule" | tr -s ' ')

# nodeclaim
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$karpenter_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$karpenter_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# overlays
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$karpenter_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml

# --- Rule 2: kubernetes.azure.com domain restriction (AKS RP sync, generated) ---
aks_rule=$(go run "${REPO_ROOT}/hack/validation/cmd/gencel" -type requirement-key)
aks_rule=${aks_rule//\"/\\\"}
aks_rule=${aks_rule//$'\n'/}
aks_rule=$(echo "$aks_rule" | tr -s ' ')

# nodeclaim
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$aks_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$aks_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# overlays
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$aks_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml
