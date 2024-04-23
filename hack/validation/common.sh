#!/usr/bin/env bash
set -euo pipefail

# remove topology.kubernetes.io/zone from allowed labels and requirements
# until the continuous drift it causes is fixed
sed -e 's|"topology.kubernetes.io/zone", ||g' -i pkg/apis/crds/karpenter.sh_nodeclaims.yaml
sed -e 's|"topology.kubernetes.io/zone", ||g' -i pkg/apis/crds/karpenter.sh_nodepools.yaml
