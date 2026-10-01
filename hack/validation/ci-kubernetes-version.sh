#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

readonly expected_version="1.34.x"
readonly workflow=".github/workflows/ci.yml"
readonly install_deps_action=".github/actions/install-deps/action.yaml"

assert_equals() {
    local actual="$1"
    local expected="$2"
    local description="$3"

    if [[ "$actual" != "$expected" ]]; then
        echo "expected ${description} to be ${expected}, got ${actual}" >&2
        exit 1
    fi
}

assert_equals \
    "$(yq eval '.jobs.ci.env.K8S_VERSION' "$workflow")" \
    "$expected_version" \
    "non-test CI job K8S_VERSION"
assert_equals \
    "$(yq eval '.jobs.ci.steps[] | select(.uses == "./.github/actions/install-deps") | .with.k8sVersion' "$workflow")" \
    '${{ env.K8S_VERSION }}' \
    "install-deps k8sVersion input"
assert_equals \
    "$(yq eval '.inputs.k8sVersion.default' "$install_deps_action")" \
    "$expected_version" \
    "install-deps default Kubernetes version"
