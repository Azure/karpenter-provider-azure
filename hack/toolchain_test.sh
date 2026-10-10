#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUBJECT="${SCRIPT_DIR}/toolchain.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

assert_status() {
    local expected="$1"
    shift
    local actual=0
    "$@" >/dev/null 2>&1 || actual=$?
    [[ "${actual}" -eq "${expected}" ]] ||
        fail "expected status ${expected}, got ${actual}: $*"
}

assert_contains() {
    local file="$1"
    local expected="$2"
    grep -Fq -- "${expected}" "${file}" ||
        fail "expected ${file} to contain '${expected}'"
}

make_version_tool() {
    local path="$1"
    local output="$2"
    local status="${3:-0}"
    mkdir -p "$(dirname "${path}")"
    cat >"${path}" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"\${VERSION_ARGS_LOG}"
printf '%s\n' '${output}'
exit ${status}
EOF
    chmod +x "${path}"
}

REAL_GO="$(command -v go)"
FAKE_BIN="${TEST_ROOT}/bin"
GO_CALL_LOG="${TEST_ROOT}/go-calls.log"
mkdir -p "${FAKE_BIN}"
cat >"${FAKE_BIN}/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "env" && "${2:-}" == "GOPATH" ]]; then
    printf '%s\n' "${FAKE_GOPATH}"
elif [[ "${1:-}" == "-C" && "${3:-}" == "list" ]]; then
    printf '%s\n' "v2.33.0"
elif [[ "${1:-}" == "install" ]]; then
    printf '%s\n' "$*" >>"${GO_CALL_LOG}"
else
    exec "${REAL_GO}" "$@"
fi
EOF
chmod +x "${FAKE_BIN}/go"

cat >"${FAKE_BIN}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
while [[ "$#" -gt 0 ]]; do
    case "$1" in
        --output)
            output="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done
cp "${SETUP_ENVTEST_FIXTURE}" "${output}"
EOF
chmod +x "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/sudo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${SUDO_CALL_LOG:-}" ]]; then
    printf '%s\n' "$*" >>"${SUDO_CALL_LOG}"
fi
if [[ "${FAIL_ON_SUDO:-false}" == true ]]; then
    exit 99
fi
exec "$@"
EOF
chmod +x "${FAKE_BIN}/sudo"

export REAL_GO FAKE_GOPATH="${TEST_ROOT}/gopath" GO_CALL_LOG
PATH="${FAKE_BIN}:${PATH}"
export PATH

# shellcheck source=hack/toolchain.sh
source "${SUBJECT}"

test_should_skip_policy_and_versions() {
    local root="${TEST_ROOT}/should-skip"
    local tool="${root}/controller-gen"
    local args_log="${root}/args.log"
    mkdir -p "${root}"
    VERSION_ARGS_LOG="${args_log}"
    export VERSION_ARGS_LOG
    make_version_tool "${tool}" "Version: v0.19.0"

    SKIP_INSTALLED=true \
        assert_status 0 should-skip "${tool}" --version v0.19.0
    SKIP_INSTALLED=true \
        assert_status 1 should-skip "${tool}" --version v0.18.0
    SKIP_INSTALLED=false \
        assert_status 1 should-skip "${tool}" --version v0.19.0
    SKIP_INSTALLED=true \
        assert_status 0 should-skip "${tool}"

    SKIP_INSTALLED=true \
        assert_status 0 should-skip "${tool}" version v0.19.0 --client=true
    assert_contains "${args_log}" "version --client=true"

    make_version_tool "${tool}" "Version: v0.19.00"
    SKIP_INSTALLED=true \
        assert_status 1 should-skip "${tool}" --version v0.19.0

    make_version_tool "${tool}" "Version: v0.190.1"
    SKIP_INSTALLED=true \
        assert_status 1 should-skip "${tool}" --version v0.19

    make_version_tool "${tool}" "Version: v0.19.0" 1
    SKIP_INSTALLED=true \
        assert_status 1 should-skip "${tool}" --version v0.19.0

    SKIP_INSTALLED=true \
        assert_status 2 should-skip "${tool}" -version v0.19.0
    SKIP_INSTALLED=true \
        assert_status 2 should-skip "${tool}" --version
}

test_go_install_forms() {
    local current="${TOOL_DEST}/current-tool"
    local missing="${TOOL_DEST}/missing-tool"
    VERSION_ARGS_LOG="${TEST_ROOT}/go-version-args.log"
    export VERSION_ARGS_LOG
    make_version_tool "${current}" "current-tool v1.2.3"
    : >"${GO_CALL_LOG}"

    SKIP_INSTALLED=true \
        go-install current-tool --version v1.2.3 example.com/current@v1.2.3
    [[ ! -s "${GO_CALL_LOG}" ]] || fail "matching tool should not be installed"

    SKIP_INSTALLED=true \
        go-install missing-tool example.com/missing@v1.0.0
    assert_contains "${GO_CALL_LOG}" "install example.com/missing@v1.0.0"

    assert_status 2 go-install invalid --version v1.0.0
    rm -f "${missing}"
}

test_ginkgo_version_resolution() {
    [[ "$(_ginkgo_version)" == "v2.33.0" ]] ||
        fail "expected Ginkgo version v2.33.0"
}

make_asset_dir() {
    local path="$1"
    local kubernetes_version="$2"
    make_version_tool "${path}/kube-apiserver" "Kubernetes v${kubernetes_version}"
    make_version_tool "${path}/kubectl" "Client Version: v${kubernetes_version}"
    make_version_tool "${path}/etcd" "etcd Version: 3.6.4"
}

make_setup_envtest() {
    local path="$1"
    local asset_dir="$2"
    cat >"${path}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >>"\${SETUP_ENVTEST_CALL_LOG}"
printf '%s\n' "${asset_dir}"
EOF
    chmod +x "${path}"
}

configure_envtest() {
    local root="$1"
    local assets="$2"
    local setup_envtest="$3"
    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${setup_envtest}"
    SETUP_ENVTEST_CALL_LOG="${root}/calls.log"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG
}

test_envtest_refreshes_the_complete_bundle() {
    local root="${TEST_ROOT}/envtest-refresh"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.34.1"
    make_asset_dir "${resolved}" "1.32.4"
    make_version_tool "${assets}/kubectl" "Client Version: v1.31.9"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    _ensure_envtest_assets
    [[ "$(readlink "${assets}/kube-apiserver")" == "${resolved}/kube-apiserver" ]] ||
        fail "expected kube-apiserver link to refresh"
    [[ "$(readlink "${assets}/kubectl")" == "${resolved}/kubectl" ]] ||
        fail "expected kubectl link to refresh"
    [[ "$(readlink "${assets}/etcd")" == "${resolved}/etcd" ]] ||
        fail "expected etcd link to refresh"
    assert_contains "${SETUP_ENVTEST_CALL_LOG}" "use --force -p path 1.32.x"
}

test_envtest_keeps_a_healthy_bundle() {
    local root="${TEST_ROOT}/envtest-healthy"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.32.4"
    make_asset_dir "${resolved}" "1.32.5"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    _ensure_envtest_assets
    [[ ! -e "${SETUP_ENVTEST_CALL_LOG}" || ! -s "${SETUP_ENVTEST_CALL_LOG}" ]] ||
        fail "healthy assets should not call setup-envtest use"
    assert_contains "${VERSION_ARGS_LOG}" "version --client=true"
}

test_missing_etcd_refreshes_every_link() {
    local root="${TEST_ROOT}/envtest-missing-etcd"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.32.4"
    make_asset_dir "${resolved}" "1.32.5"
    rm "${assets}/etcd"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    _ensure_envtest_assets
    for binary in kube-apiserver kubectl etcd; do
        [[ "$(readlink "${assets}/${binary}")" == "${resolved}/${binary}" ]] ||
            fail "expected ${binary} to refresh with the bundle"
    done
}

test_directory_shaped_etcd_refreshes_every_link() {
    local root="${TEST_ROOT}/envtest-directory-etcd"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.32.4"
    make_asset_dir "${resolved}" "1.32.5"
    rm "${assets}/etcd"
    mkdir "${assets}/etcd"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    _ensure_envtest_assets
    for binary in kube-apiserver kubectl etcd; do
        [[ "$(readlink "${assets}/${binary}")" == "${resolved}/${binary}" ]] ||
            fail "expected directory-shaped ${binary} cache entry to refresh with the bundle"
    done
}

test_mixed_envtest_bundle_refreshes_every_link() {
    local root="${TEST_ROOT}/envtest-mixed-bundle"
    local assets="${root}/assets"
    local bundle_a="${root}/bundle-a"
    local bundle_b="${root}/bundle-b"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${bundle_a}" "${bundle_b}" "${resolved}"
    make_asset_dir "${bundle_a}" "1.32.4"
    make_asset_dir "${bundle_b}" "1.32.4"
    make_asset_dir "${resolved}" "1.32.5"
    ln -s "${bundle_a}/kube-apiserver" "${assets}/kube-apiserver"
    ln -s "${bundle_a}/kubectl" "${assets}/kubectl"
    ln -s "${bundle_b}/etcd" "${assets}/etcd"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    _ensure_envtest_assets
    for binary in kube-apiserver kubectl etcd; do
        [[ "$(readlink "${assets}/${binary}")" == "${resolved}/${binary}" ]] ||
            fail "expected mixed ${binary} link to refresh with one bundle"
    done
}

test_rejects_an_incomplete_resolved_bundle() {
    local root="${TEST_ROOT}/envtest-incomplete"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.31.9"
    make_asset_dir "${resolved}" "1.32.4"
    rm "${resolved}/kubectl"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    assert_status 1 _ensure_envtest_assets
}

test_rejects_a_wrong_version_after_refresh() {
    local root="${TEST_ROOT}/envtest-wrong-version"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.31.8"
    make_asset_dir "${resolved}" "1.31.9"
    make_setup_envtest "${setup_envtest}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${setup_envtest}"

    assert_status 1 _ensure_envtest_assets
}

test_kubebuilder_always_downloads_setup_envtest() {
    local root="${TEST_ROOT}/kubebuilder-helper"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local fixture="${root}/setup-envtest-fixture"
    local installed="${root}/bin/setup-envtest"
    mkdir -p "${assets}" "${resolved}" "$(dirname "${installed}")"
    make_asset_dir "${assets}" "1.32.4"
    make_asset_dir "${resolved}" "1.32.5"
    make_setup_envtest "${fixture}" "${resolved}"
    configure_envtest "${root}" "${assets}" "${installed}"
    SETUP_ENVTEST_FIXTURE="${fixture}"
    SETUP_ENVTEST_SHA256="$(sha256sum "${fixture}" | awk '{print $1}')"
    SUDO_CALL_LOG="${root}/sudo-calls.log"
    FAIL_ON_SUDO=true
    export SETUP_ENVTEST_FIXTURE SETUP_ENVTEST_SHA256 SUDO_CALL_LOG FAIL_ON_SUDO

    kubebuilder
    [[ -x "${installed}" ]] || fail "expected setup-envtest to be downloaded"
    [[ ! -e "${SETUP_ENVTEST_CALL_LOG}" || ! -s "${SETUP_ENVTEST_CALL_LOG}" ]] ||
        fail "healthy assets should not be resolved again"
    [[ ! -e "${SUDO_CALL_LOG}" || ! -s "${SUDO_CALL_LOG}" ]] ||
        fail "writable envtest paths should not require sudo"
}

test_setup_envtest_digest_mismatch_preserves_existing_helper() {
    local root="${TEST_ROOT}/setup-envtest-digest"
    local fixture="${root}/setup-envtest-fixture"
    local installed="${root}/bin/setup-envtest"
    mkdir -p "$(dirname "${installed}")"
    cat >"${installed}" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' original
EOF
    chmod +x "${installed}"
    cat >"${fixture}" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' replacement
EOF
    chmod +x "${fixture}"

    SETUP_ENVTEST_BIN="${installed}"
    SETUP_ENVTEST_FIXTURE="${fixture}"
    SETUP_ENVTEST_SHA256="0000000000000000000000000000000000000000000000000000000000000000"
    export SETUP_ENVTEST_BIN SETUP_ENVTEST_FIXTURE SETUP_ENVTEST_SHA256

    assert_status 1 _install_setup_envtest
    [[ "$("${installed}")" == "original" ]] ||
        fail "digest mismatch replaced the existing setup-envtest helper"
}

test_should_skip_policy_and_versions
test_go_install_forms
test_ginkgo_version_resolution
test_envtest_refreshes_the_complete_bundle
test_envtest_keeps_a_healthy_bundle
test_missing_etcd_refreshes_every_link
test_directory_shaped_etcd_refreshes_every_link
test_mixed_envtest_bundle_refreshes_every_link
test_rejects_an_incomplete_resolved_bundle
test_rejects_a_wrong_version_after_refresh
test_kubebuilder_always_downloads_setup_envtest
test_setup_envtest_digest_mismatch_preserves_existing_helper
echo "PASS: toolchain helper tests"
