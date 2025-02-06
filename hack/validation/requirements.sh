#!/bin/bash
set -euo pipefail

# requirements validation for nodeclaim and nodepool
# checking for restricted labels while filtering out well known labels

rule=$'self in
    [
        "karpenter.azure.com/sku-name",
        "karpenter.azure.com/sku-family",
        "karpenter.azure.com/sku-version",
        "karpenter.azure.com/sku-cpu",
        "karpenter.azure.com/sku-memory",
        "karpenter.azure.com/sku-accelerator",
        "karpenter.azure.com/sku-networking-accelerated",
        "karpenter.azure.com/sku-storage-premium-capable",
        "karpenter.azure.com/sku-storage-ephemeralos-maxsize",
        "karpenter.azure.com/sku-encryptionathost-capable",
        "karpenter.azure.com/sku-gpu-name",
        "karpenter.azure.com/sku-gpu-manufacturer",
        "karpenter.azure.com/sku-gpu-count"
    ]
    || !self.find("^([^/]+)").endsWith("karpenter.azure.com")
'
# above regex: everything before the first '/' (any characters except '/' at the beginning of the string)

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 2 entries
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml)  -eq 2 ]] || { echo "expected two versions"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 2 ]] || { echo "expected two versions"; exit 1; }

# v1beta1
# nodeclaim
printf -v expr '.spec.versions[1].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool
printf -v expr '.spec.versions[1].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# v1
# nodeclaim
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
