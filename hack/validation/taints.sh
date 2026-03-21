#!/bin/bash
set -euo pipefail

# taints validation for nodepool
# checking for restricted taint key domains while filtering out well known taint keys

# kubernetes.azure.com domain restriction for taint keys
# These are the only kubernetes.azure.com taint keys users may set on NodePool;
# all others are system-assigned and will be rejected by AKS Machine API.
# Using startsWith instead of find+endsWith for lower CEL cost estimation.
taint_rule=$'self.all(x, !x.key.startsWith("kubernetes.azure.com/")
    || x.key in
    [
        "kubernetes.azure.com/scalesetpriority"
    ]
)
'

taint_rule=${taint_rule//\"/\\\"}            # escape double quotes
taint_rule=${taint_rule//$'\n'/}             # remove newlines
taint_rule=$(echo "$taint_rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# set maxItems on taints and startupTaints to allow CEL cost estimation to pass
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.maxItems = 50' \
    -i pkg/apis/crds/karpenter.sh_nodepools.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.maxItems = 50' \
    -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool taints - kubernetes.azure.com
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$taint_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool startupTaints - kubernetes.azure.com
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.x-kubernetes-validations +=
    [{"message": "taint key domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$taint_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml
