#!/bin/bash
set -euo pipefail

# requirements validation for nodeclaim and nodepool
# checking for restricted labels while filtering out well known labels

rule=$'self in
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
        "karpenter.azure.com/sku-gpu-count",
        "karpenter.azure.com/placement-scope"
    ]
    || !self.find("^([^/]+)").endsWith("karpenter.azure.com")
'
# above regex: everything before the first '/' (any characters except '/' at the beginning of the string)

rule=${rule//\"/\\\"}            # escape double quotes
rule=${rule//$'\n'/}             # remove newlines
rule=$(echo "$rule" | tr -s ' ') # remove extra spaces

# kubernetes.azure.com domain restriction
# We allow well-known labels as well as ebpf-dataplane (required for Cilium)
# We allow kubernetes.azure.com/hostedvm as it's used by some deployments in HOBO in a NotIn targeting Karpenter nodes
# We allow kubernetes.azure.com/ebpf-host-routing DoesNotExist as Cilium may request that
# We allow kubernetes.azure.com/network-policy as Azure CNI requests NotIn ["none"] for it at the deployment level
aks_rule=$'self in
    [
        "kubernetes.azure.com/mode",
        "kubernetes.azure.com/scalesetpriority",
        "kubernetes.azure.com/priority",
        "kubernetes.azure.com/fips_enabled",
        "kubernetes.azure.com/os-sku",
        "kubernetes.azure.com/cluster",
        "kubernetes.azure.com/sku-cpu",
        "kubernetes.azure.com/sku-memory",
        "kubernetes.azure.com/ebpf-dataplane",
        "kubernetes.azure.com/ebpf-host-routing",
        "kubernetes.azure.com/network-policy",
        "kubernetes.azure.com/hostedvm",
    ]
    || !self.find("^([^/]+)").endsWith("kubernetes.azure.com")
'

aks_rule=${aks_rule//\"/\\\"}            # escape double quotes
aks_rule=${aks_rule//$'\n'/}             # remove newlines
aks_rule=$(echo "$aks_rule" | tr -s ' ') # remove extra spaces

# agentpool restriction
agentpool_rule=$'self != \x27agentpool\x27'

agentpool_rule=${agentpool_rule//\"/\\\"}            # escape double quotes
agentpool_rule=${agentpool_rule//$'\n'/}             # remove newlines
agentpool_rule=$(echo "$agentpool_rule" | tr -s ' ') # remove extra spaces

# storageprofile restriction
storageprofile_rule=$'self != \x27storageprofile\x27'

storageprofile_rule=${storageprofile_rule//\"/\\\"}            # escape double quotes
storageprofile_rule=${storageprofile_rule//$'\n'/}             # remove newlines
storageprofile_rule=$(echo "$storageprofile_rule" | tr -s ' ') # remove extra spaces

# storagetier restriction
storagetier_rule=$'self != \x27storagetier\x27'

storagetier_rule=${storagetier_rule//\"/\\\"}            # escape double quotes
storagetier_rule=${storagetier_rule//$'\n'/}             # remove newlines
storagetier_rule=$(echo "$storagetier_rule" | tr -s ' ') # remove extra spaces

# accelerator restriction
accelerator_rule=$'self != \x27accelerator\x27'

accelerator_rule=${accelerator_rule//\"/\\\"}            # escape double quotes
accelerator_rule=${accelerator_rule//$'\n'/}             # remove newlines
accelerator_rule=$(echo "$accelerator_rule" | tr -s ' ') # remove extra spaces

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml)  -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeoverlays.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# nodeclaim
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$aks_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"agentpool\\" is restricted", "rule": "%s"}]' "$agentpool_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"storageprofile\\" is restricted", "rule": "%s"}]' "$storageprofile_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"storagetier\\" is restricted", "rule": "%s"}]' "$storagetier_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"accelerator\\" is restricted", "rule": "%s"}]' "$accelerator_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodepool
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$aks_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"agentpool\\" is restricted", "rule": "%s"}]' "$agentpool_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"storageprofile\\" is restricted", "rule": "%s"}]' "$storageprofile_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"storagetier\\" is restricted", "rule": "%s"}]' "$storagetier_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"accelerator\\" is restricted", "rule": "%s"}]' "$accelerator_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# overlays
printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"karpenter.azure.com\\" is restricted", "rule": "%s"}]' "$rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label domain \\"kubernetes.azure.com\\" is restricted", "rule": "%s"}]' "$aks_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"agentpool\\" is restricted", "rule": "%s"}]' "$agentpool_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"storageprofile\\" is restricted", "rule": "%s"}]' "$storageprofile_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"storagetier\\" is restricted", "rule": "%s"}]' "$storagetier_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml

printf -v expr '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.requirements.items.properties.key.x-kubernetes-validations +=
    [{"message": "label \\"accelerator\\" is restricted", "rule": "%s"}]' "$accelerator_rule"
yq eval "${expr}" -i pkg/apis/crds/karpenter.sh_nodeoverlays.yaml
