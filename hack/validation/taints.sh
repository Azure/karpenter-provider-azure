#!/bin/bash
set -euo pipefail

# taints validation for nodepool and nodeclaim
# blocking kubernetes.azure.com/ prefix on taint keys for both taints and startupTaints
# This aligns with AKS Machine API validation which blocks this prefix on taint keys.
# System taints under this prefix (e.g., scalesetpriority, mode) are assigned server-side
# by AKS Machine API, not by users through NodePool CRDs.

# CEL rule: the taint key must not start with "kubernetes.azure.com/"
# Using startsWith for simplicity and low CEL cost estimation (avoids regex).
rule='!self.startsWith(\"kubernetes.azure.com/\")'

message='taint key domain \"kubernetes.azure.com\" is restricted'

# check that .spec.versions has 1 entry
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodepools.yaml)  -eq 1 ]] || { echo "expected one version"; exit 1; }
[[ $(yq e '.spec.versions | length' pkg/apis/crds/karpenter.sh_nodeclaims.yaml) -eq 1 ]] || { echo "expected one version"; exit 1; }

# nodepool taints
yq eval ".spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations += [{\"message\": \"${message}\", \"rule\": \"${rule}\"}]" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodepool startupTaints
yq eval ".spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations += [{\"message\": \"${message}\", \"rule\": \"${rule}\"}]" -i pkg/apis/crds/karpenter.sh_nodepools.yaml

# nodeclaim taints
yq eval ".spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.taints.items.properties.key.x-kubernetes-validations += [{\"message\": \"${message}\", \"rule\": \"${rule}\"}]" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# nodeclaim startupTaints
yq eval ".spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.startupTaints.items.properties.key.x-kubernetes-validations += [{\"message\": \"${message}\", \"rule\": \"${rule}\"}]" -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
