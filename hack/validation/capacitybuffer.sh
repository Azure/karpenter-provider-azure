#!/usr/bin/env bash
set -euo pipefail

# Validates the CapacityBuffer chart compatibility and CRD packaging contract.
#
# This complements, rather than duplicates, runtime tests:
# - feature-gate rendering is safe for defaults, explicit boolean/string values,
#   and legacy Helm values that do not contain the newly added key;
# - public and generated self-hosted values remain default-disabled;
# - CapacityBuffer and PodTemplate RBAC is present only when the gate is enabled;
# - both CRD delivery paths contain exactly one v1beta1 CapacityBuffer CRD; and
# - every packaged CRD is byte-for-byte identical to the pinned Karpenter core CRD.
# Runtime provisioning and lifecycle behavior belongs in the CapacityBuffer E2E suite.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

disabled_render="$tmp_dir/disabled.yaml"
explicitly_disabled_render="$tmp_dir/explicitly-disabled.yaml"
string_disabled_render="$tmp_dir/string-disabled.yaml"
enabled_render="$tmp_dir/enabled.yaml"
string_enabled_render="$tmp_dir/string-enabled.yaml"
legacy_render="$tmp_dir/legacy.yaml"
legacy_chart="$tmp_dir/karpenter"
standalone_crd_render="$tmp_dir/standalone-crds.yaml"
main_crd_render="$tmp_dir/main-crds.yaml"
helm_stderr="$tmp_dir/helm.stderr"

run_helm() {
    local output="$1"
    shift
    if ! helm "$@" >"$output" 2>"$helm_stderr"; then
        cat "$helm_stderr" >&2
        return 1
    fi
}

run_helm /dev/null lint charts/karpenter
run_helm /dev/null lint charts/karpenter-crd

# Public and generated self-hosted values remain opt-in. The dedicated E2E
# suite enables the gate and temporary RBAC dynamically when it runs in-cluster.
[[ "$(yq eval '.settings.featureGates.capacityBuffer' charts/karpenter/values.yaml)" == "false" ]] || {
    echo "expected the public chart to default CapacityBuffer to false"
    exit 1
}
[[ "$(yq eval '.settings.featureGates.capacityBuffer' karpenter-values-template.yaml)" == "false" ]] || {
    echo "expected generated self-hosted values to default CapacityBuffer to false"
    exit 1
}

# Render the chart feature gate using the supported Helm value shapes.
run_helm "$disabled_render" template karpenter charts/karpenter --namespace karpenter
run_helm "$explicitly_disabled_render" template karpenter charts/karpenter --namespace karpenter \
    --set settings.featureGates.capacityBuffer=false
run_helm "$string_disabled_render" template karpenter charts/karpenter --namespace karpenter \
    --set-string settings.featureGates.capacityBuffer=false
run_helm "$enabled_render" template karpenter charts/karpenter --namespace karpenter \
    --set settings.featureGates.capacityBuffer=true
run_helm "$string_enabled_render" template karpenter charts/karpenter --namespace karpenter \
    --set-string settings.featureGates.capacityBuffer=true
cp -LR charts/karpenter "$legacy_chart"
yq eval -i 'del(.settings.featureGates.capacityBuffer)' "$legacy_chart/values.yaml"
run_helm "$legacy_render" template karpenter "$legacy_chart" --namespace karpenter

# Render both supported CRD delivery paths.
run_helm "$standalone_crd_render" template karpenter-crd charts/karpenter-crd
run_helm "$main_crd_render" template karpenter charts/karpenter --namespace karpenter \
    --include-crds

feature_gates() {
    yq eval-all 'select(.kind == "Deployment") | .spec.template.spec.containers[] | .env[] | select(.name == "FEATURE_GATES") | .value' "$1"
}

[[ "$(feature_gates "$disabled_render")" == *"CapacityBuffer=false"* ]] || {
    echo "expected CapacityBuffer=false in the default FEATURE_GATES value"
    exit 1
}
[[ "$(feature_gates "$explicitly_disabled_render")" == *"CapacityBuffer=false"* ]] || {
    echo "expected CapacityBuffer=false when the chart setting is explicitly disabled"
    exit 1
}
[[ "$(feature_gates "$string_disabled_render")" == *"CapacityBuffer=false"* ]] || {
    echo "expected CapacityBuffer=false when Helm values contain the string false"
    exit 1
}
[[ "$(feature_gates "$legacy_render")" == *"CapacityBuffer=false"* ]] || {
    echo "expected CapacityBuffer=false when legacy values omit the chart setting"
    exit 1
}
[[ "$(feature_gates "$enabled_render")" == *"CapacityBuffer=true"* ]] || {
    echo "expected CapacityBuffer=true when the chart setting is enabled"
    exit 1
}

core_role_json() {
    yq eval-all -o=json 'select(.kind == "ClusterRole" and .metadata.name == "karpenter-core")' "$1"
}

disabled_rules="$(core_role_json "$disabled_render")"
string_disabled_rules="$(core_role_json "$string_disabled_render")"
enabled_rules="$(core_role_json "$enabled_render")"
string_enabled_rules="$(core_role_json "$string_enabled_render")"

# Disabled values must not broaden the controller role; enabled values must
# grant the exact read/status permissions used by core.
jq -e 'all(.rules[]; (.apiGroups | index("autoscaling.x-k8s.io")) == null and (.resources | index("podtemplates")) == null)' \
    <<<"$disabled_rules" >/dev/null
jq -e 'all(.rules[]; (.apiGroups | index("autoscaling.x-k8s.io")) == null and (.resources | index("podtemplates")) == null)' \
    <<<"$string_disabled_rules" >/dev/null
jq -e 'any(.rules[]; .apiGroups == ["autoscaling.x-k8s.io"] and .resources == ["capacitybuffers"] and .verbs == ["get", "list", "watch"])' \
    <<<"$enabled_rules" >/dev/null
jq -e 'any(.rules[]; .apiGroups == ["autoscaling.x-k8s.io"] and .resources == ["capacitybuffers/status"] and .verbs == ["update", "patch"])' \
    <<<"$enabled_rules" >/dev/null
jq -e 'any(.rules[]; .apiGroups == [""] and .resources == ["podtemplates"] and .verbs == ["get", "list", "watch"])' \
    <<<"$enabled_rules" >/dev/null
jq -e 'any(.rules[]; .apiGroups == ["autoscaling.x-k8s.io"] and .resources == ["capacitybuffers"] and .verbs == ["get", "list", "watch"])' \
    <<<"$string_enabled_rules" >/dev/null

for render in "$standalone_crd_render" "$main_crd_render"; do
    count="$(yq eval-all -o=json 'select(.kind == "CustomResourceDefinition" and .metadata.name == "capacitybuffers.autoscaling.x-k8s.io")' "$render" | jq -s length)"
    [[ "$count" -eq 1 ]] || {
        echo "expected exactly one CapacityBuffer CRD in $render, found $count"
        exit 1
    }
done

core_crd="$(go list -m -f '{{.Dir}}' sigs.k8s.io/karpenter)/pkg/apis/crds/autoscaling.x-k8s.io_capacitybuffers.yaml"
# The module dependency is authoritative. Generated/copied/chart paths must
# retain its API version and exact schema.
for crd in \
    "$core_crd" \
    pkg/apis/crds/autoscaling.x-k8s.io_capacitybuffers.yaml \
    charts/karpenter-crd/templates/autoscaling.x-k8s.io_capacitybuffers.yaml \
    charts/karpenter/crds/autoscaling.x-k8s.io_capacitybuffers.yaml; do
    storage_version="$(yq eval '.spec.versions[] | select(.storage == true) | .name' "$crd")"
    [[ "$(yq eval '.spec.group' "$crd")" == "autoscaling.x-k8s.io" && "$storage_version" == "v1beta1" ]] || {
        echo "expected autoscaling.x-k8s.io/v1beta1 storage version in $crd"
        exit 1
    }
    cmp "$core_crd" "$crd"
done
