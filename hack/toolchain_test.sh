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

    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 0 should-skip "${tool}" --version v0.19.0
    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 1 should-skip "${tool}" --version v0.18.0
    SKIP_INSTALLED=true FORCE_INSTALL=true \
        assert_status 1 should-skip "${tool}" --version v0.19.0
    SKIP_INSTALLED=false FORCE_INSTALL=false \
        assert_status 1 should-skip "${tool}" --version v0.19.0
    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 0 should-skip "${tool}"

    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 0 should-skip "${tool}" version v0.19.0 --client=true
    assert_contains "${args_log}" "version --client=true"

    make_version_tool "${tool}" "Version: v0.19.0" 1
    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 1 should-skip "${tool}" --version v0.19.0

    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 2 should-skip "${tool}" -version v0.19.0
    SKIP_INSTALLED=true FORCE_INSTALL=false \
        assert_status 2 should-skip "${tool}" --version
}

test_go_install_forms() {
    local current="${TOOL_DEST}/current-tool"
    local missing="${TOOL_DEST}/missing-tool"
    VERSION_ARGS_LOG="${TEST_ROOT}/go-version-args.log"
    export VERSION_ARGS_LOG
    make_version_tool "${current}" "current-tool v1.2.3"
    : >"${GO_CALL_LOG}"

    SKIP_INSTALLED=true FORCE_INSTALL=false \
        go-install current-tool --version v1.2.3 example.com/current@v1.2.3
    [[ ! -s "${GO_CALL_LOG}" ]] || fail "matching tool should not be installed"

    SKIP_INSTALLED=true FORCE_INSTALL=false \
        go-install missing-tool example.com/missing@v1.0.0
    assert_contains "${GO_CALL_LOG}" "install example.com/missing@v1.0.0"

    assert_status 2 go-install invalid --version v1.0.0
    rm -f "${missing}"
}

test_ginkgo_version_resolution() {
    [[ "$(_ginkgo_version)" == "v2.33.0" ]] ||
        fail "expected Ginkgo version v2.33.0"
}

test_should_skip_policy_and_versions
test_go_install_forms
test_ginkgo_version_resolution
echo "PASS: toolchain helper tests"
