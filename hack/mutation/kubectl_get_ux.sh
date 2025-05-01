#!/bin/bash
set -euo pipefail

# remove ID & ImageID from additionalPrinterColumns; the values are too long to be useful
yq -i 'del(.spec.versions[0].additionalPrinterColumns[] | select (.name=="ID"))' \
    pkg/apis/crds/karpenter.sh_nodeclaims.yaml
yq -i 'del(.spec.versions[0].additionalPrinterColumns[] | select (.name=="ImageID"))' \
    pkg/apis/crds/karpenter.sh_nodeclaims.yaml

# enable "kubectl get nap"
yq -i '(.spec.names.categories) = ["karpenter","nap"]' \
    pkg/apis/crds/karpenter.sh_nodeclaims.yaml
yq -i '(.spec.names.categories) = ["karpenter","nap"]' \
    pkg/apis/crds/karpenter.sh_nodepools.yaml
