#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUBJECT="${SCRIPT_DIR}/setup-envtest.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

assert_contains() {
    local file="$1"
    local expected="$2"
    grep -Fq -- "${expected}" "${file}" || fail "expected ${file} to contain '${expected}'"
}

assert_not_contains() {
    local file="$1"
    local unexpected="$2"
    if grep -Fq -- "${unexpected}" "${file}"; then
        fail "expected ${file} not to contain '${unexpected}'"
    fi
}

make_executable() {
    local path="$1"
    local output="$2"
    mkdir -p "$(dirname "${path}")"
    cat > "${path}" <<EOF
#!/usr/bin/env bash
echo "${output}"
EOF
    chmod +x "${path}"
}

make_asset_dir() {
    local path="$1"
    local version="$2"
    make_executable "${path}/kube-apiserver" "Kubernetes v${version}"
    make_executable "${path}/kubectl" "kubectl v${version}"
    make_executable "${path}/etcd" "etcd v3.5.0"
}

make_setup_envtest() {
    local path="$1"
    local version="${2:-v0.22.3}"
    cat > "${path}" <<EOF
#!/usr/bin/env bash
set -euo pipefail

if [[ "\${1:-}" == "version" ]]; then
    echo "setup-envtest version: ${version}"
    exit
fi
EOF
    cat >> "${path}" <<'EOF'
echo "$*" >> "${CALL_LOG}"
if [[ " $* " == *" --force "* ]]; then
    if [[ -n "${FORCE_ASSET_DIR:-}" ]]; then
        cp "${FORCE_ASSET_DIR}/kube-apiserver" "${ASSET_DIR}/kube-apiserver"
        cp "${FORCE_ASSET_DIR}/kubectl" "${ASSET_DIR}/kubectl"
        cp "${FORCE_ASSET_DIR}/etcd" "${ASSET_DIR}/etcd"
        chmod +x "${ASSET_DIR}/kube-apiserver" "${ASSET_DIR}/kubectl" "${ASSET_DIR}/etcd"
    elif [[ -n "${FORCE_SOURCE:-}" ]]; then
        cp "${FORCE_SOURCE}" "${ASSET_DIR}/kube-apiserver"
        chmod +x "${ASSET_DIR}/kube-apiserver"
    fi
fi
echo "${ASSET_DIR}"
EOF
    chmod +x "${path}"
}

run_setup() {
    local assets_root="$1"
    local asset_dir="$2"
    local setup_envtest="$3"
    local call_log="$4"
    local force_source="${5:-}"
    local force_asset_dir="${6:-}"
    local k8s_version="${7:-1.32.x}"
    local setup_envtest_url="${8:-}"
    local kubernetes_download_base_url="${9:-https://dl.k8s.io}"

    K8S_VERSION="${k8s_version}" \
        KUBEBUILDER_ASSETS="${assets_root}" \
        SETUP_ENVTEST_BIN="${setup_envtest}" \
        SETUP_ENVTEST_URL="${setup_envtest_url}" \
        KUBERNETES_DOWNLOAD_BASE_URL="${kubernetes_download_base_url}" \
        CALL_LOG="${call_log}" \
        ASSET_DIR="${asset_dir}" \
        FORCE_SOURCE="${force_source}" \
        FORCE_ASSET_DIR="${force_asset_dir}" \
        bash "${SUBJECT}"
}

test_reuses_valid_cached_assets_and_repairs_stale_links() {
    local root="${TEST_ROOT}/valid"
    local assets_root="${root}/assets"
    local asset_dir="${assets_root}/k8s/1.32.0-linux-amd64"
    local setup_envtest="${root}/setup-envtest"
    local call_log="${root}/calls.log"

    mkdir -p "${root}" "${assets_root}"
    make_asset_dir "${asset_dir}" "1.32.0"
    make_executable "${assets_root}/kube-apiserver" "Kubernetes v1.34.1"
    make_setup_envtest "${setup_envtest}"

    run_setup "${assets_root}" "${asset_dir}" "${setup_envtest}" "${call_log}"

    [[ "$("${assets_root}/kube-apiserver" --version)" == "Kubernetes v1.32.0" ]] ||
        fail "expected the top-level kube-apiserver link to use Kubernetes v1.32.0"
    assert_contains "${call_log}" "use -i -p path 1.32.x"
    assert_not_contains "${call_log}" "--force"
}

test_repairs_cached_assets_with_the_wrong_version() {
    local root="${TEST_ROOT}/repair"
    local assets_root="${root}/assets"
    local asset_dir="${assets_root}/k8s/1.32.0-linux-amd64"
    local setup_envtest="${root}/setup-envtest"
    local call_log="${root}/calls.log"
    local repaired_binary="${root}/kube-apiserver-1.32"
    local output="${root}/output.log"

    mkdir -p "${root}" "${assets_root}"
    make_asset_dir "${asset_dir}" "1.34.1"
    make_executable "${repaired_binary}" "Kubernetes v1.32.0"
    make_setup_envtest "${setup_envtest}"

    run_setup "${assets_root}" "${asset_dir}" "${setup_envtest}" "${call_log}" "${repaired_binary}" >"${output}"

    [[ "$("${assets_root}/kube-apiserver" --version)" == "Kubernetes v1.32.0" ]] ||
        fail "expected repair to install Kubernetes v1.32.0"
    assert_contains "${call_log}" "--force"
    assert_contains "${output}" "Cached kube-apiserver version Kubernetes v1.34.1 does not match 1.32.x"
}

test_rejects_a_forced_install_with_the_wrong_version() {
    local root="${TEST_ROOT}/failed-repair"
    local assets_root="${root}/assets"
    local asset_dir="${assets_root}/k8s/1.32.0-linux-amd64"
    local setup_envtest="${root}/setup-envtest"
    local call_log="${root}/calls.log"
    local output="${root}/output.log"

    mkdir -p "${root}" "${assets_root}"
    make_asset_dir "${asset_dir}" "1.34.1"
    make_setup_envtest "${setup_envtest}"

    if run_setup "${assets_root}" "${asset_dir}" "${setup_envtest}" "${call_log}" >"${output}" 2>&1; then
        fail "expected setup to reject Kubernetes v1.34.1 for K8S_VERSION=1.32.x"
    fi

    assert_contains "${output}" "requested Kubernetes version: 1.32.x"
    assert_contains "${output}" "actual kube-apiserver version: Kubernetes v1.34.1"
}

test_repairs_partially_missing_cached_assets() {
    local binary

    for binary in kube-apiserver kubectl etcd; do
        local root="${TEST_ROOT}/missing-${binary}"
        local assets_root="${root}/assets"
        local asset_dir="${assets_root}/k8s/1.32.0-linux-amd64"
        local repaired_assets="${root}/repaired"
        local setup_envtest="${root}/setup-envtest"
        local call_log="${root}/calls.log"

        mkdir -p "${root}" "${assets_root}"
        make_asset_dir "${asset_dir}" "1.32.0"
        make_asset_dir "${repaired_assets}" "1.32.0"
        rm "${asset_dir}/${binary}"
        make_setup_envtest "${setup_envtest}"

        run_setup "${assets_root}" "${asset_dir}" "${setup_envtest}" "${call_log}" "" "${repaired_assets}"

        assert_contains "${call_log}" "--force"
        [[ -x "${assets_root}/${binary}" ]] || fail "expected repair to restore ${binary}"
    done
}

test_replaces_an_unpinned_setup_envtest_binary() {
    local root="${TEST_ROOT}/unpinned-helper"
    local assets_root="${root}/assets"
    local asset_dir="${assets_root}/k8s/1.32.0-linux-amd64"
    local setup_envtest="${root}/setup-envtest"
    local replacement="${root}/setup-envtest-v0.22.3"
    local call_log="${root}/calls.log"

    mkdir -p "${root}" "${assets_root}"
    make_asset_dir "${asset_dir}" "1.32.0"
    make_setup_envtest "${setup_envtest}" "v0.20.0"
    make_setup_envtest "${replacement}" "v0.22.3"

    run_setup "${assets_root}" "${asset_dir}" "${setup_envtest}" "${call_log}" "" "" "1.32.x" "file://${replacement}"

    [[ "$("${setup_envtest}" version)" == "setup-envtest version: v0.22.3" ]] ||
        fail "expected setup-envtest v0.22.3 to replace the unpinned helper"
}

test_preserves_the_kubernetes_125_cel_fix() {
    local root="${TEST_ROOT}/kubernetes-125"
    local assets_root="${root}/assets"
    local asset_dir="${assets_root}/k8s/1.25.0-linux-amd64"
    local setup_envtest="${root}/setup-envtest"
    local call_log="${root}/calls.log"
    local downloads="${root}/downloads"

    mkdir -p "${root}" "${assets_root}"
    make_asset_dir "${asset_dir}" "1.25.0"
    make_setup_envtest "${setup_envtest}"
    make_executable "${downloads}/v1.25.16/bin/linux/amd64/kube-apiserver" "Kubernetes v1.25.16"
    make_executable "${downloads}/v1.25.16/bin/linux/amd64/kubectl" "kubectl v1.25.16"

    run_setup "${assets_root}" "${asset_dir}" "${setup_envtest}" "${call_log}" "" "" \
        "1.25.x" "" "file://${downloads}"

    [[ "$("${assets_root}/kube-apiserver" --version)" == "Kubernetes v1.25.16" ]] ||
        fail "expected the Kubernetes 1.25.16 CEL fix"
}

test_reuses_valid_cached_assets_and_repairs_stale_links
test_repairs_cached_assets_with_the_wrong_version
test_rejects_a_forced_install_with_the_wrong_version
test_repairs_partially_missing_cached_assets
test_replaces_an_unpinned_setup_envtest_binary
test_preserves_the_kubernetes_125_cel_fix

echo "PASS: setup-envtest cache validation and repair"
