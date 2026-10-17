#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

default_render="$tmp_dir/default.yaml"
empty_render="$tmp_dir/empty.yaml"
ahub_render="$tmp_dir/ahub.yaml"

helm template karpenter charts/karpenter --namespace karpenter >"$default_render"
helm template karpenter charts/karpenter --namespace karpenter \
    --set-string settings.windowsLicenseType= >"$empty_render"
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

mapfile -t empty_values < <(license_values "$empty_render")
[[ "${#empty_values[@]}" -eq 1 && -z "${empty_values[0]}" ]] || {
    echo "expected an explicit empty windowsLicenseType to reach option validation unchanged"
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

grep -Fq 'settings.windowsLicenseType' docs/windows-pricing.md
grep -Fq 'Use the script, template, and chart from the same release' docs/windows-pricing.md
grep -Fq 'Existing self-hosted installations must set this value before upgrading' docs/windows-pricing.md
