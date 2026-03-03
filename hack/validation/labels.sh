#!/bin/bash
set -euo pipefail

# labels validation for nodepool
# checking for restricted labels while filtering out well known labels

rule=$'self.all(x, x in
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
    || !x.find("^([^/]+)").endsWith("karpenter.azure.com")
)
'
# above regex: everything before the first '/' (any characters except '/' at the beginning of the string)

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# nodepool
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.metadata.properties.labels.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
