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

test_should_skip_policy_and_versions
echo "PASS: toolchain helper tests"
