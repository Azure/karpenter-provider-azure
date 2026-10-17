#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

default_render="$tmp_dir/default.yaml"
ahub_render="$tmp_dir/ahub.yaml"

helm template karpenter charts/karpenter --namespace karpenter >"$default_render"
helm template karpenter charts/karpenter --namespace karpenter \
    --set settings.windowsLicenseType=Windows_Server >"$ahub_render"

license_values() {
    yq eval-all 'select(.kind == "Deployment") | .spec.template.spec.containers[] | .env[] | select(.name == "WINDOWS_LICENSE_TYPE") | .value' "$1"
}

mapfile -t default_values < <(license_values "$default_render")
[[ "${#default_values[@]}" -eq 1 && "${default_values[0]}" == "None" ]] || {
    echo "expected exactly one default WINDOWS_LICENSE_TYPE=None entry"
    exit 1
}

mapfile -t ahub_values < <(license_values "$ahub_render")
[[ "${#ahub_values[@]}" -eq 1 && "${ahub_values[0]}" == "Windows_Server" ]] || {
    echo "expected exactly one WINDOWS_LICENSE_TYPE=Windows_Server entry"
    exit 1
}

[[ "$(yq eval '.settings.windowsLicenseType' charts/karpenter/values.yaml)" == "None" ]] || {
    echo "expected the public chart to default windowsLicenseType to None"
    exit 1
}

[[ "$(yq eval '.settings.windowsLicenseType' karpenter-values-template.yaml)" == '${WINDOWS_LICENSE_TYPE}' ]] || {
    echo "expected generated self-hosted values to carry WINDOWS_LICENSE_TYPE through settings"
    exit 1
}
