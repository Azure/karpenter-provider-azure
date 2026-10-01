# Version-aware toolchain installation implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make cached toolchain installs self-heal when a required executable is missing or at the wrong version, without adding a separate envtest repair subsystem.

**Architecture:** Extend `hack/toolchain.sh` with one policy-aware `should-skip` API and a smaller policy-free command/version predicate. Go tools and Trivy use the same decision, while envtest treats `kube-apiserver`, `kubectl`, and `etcd` as one bundle and refreshes all three when any check fails. CI always runs the toolchain: cache misses disable skipping and cache hits validate before skipping.

**Tech Stack:** Bash, GNU command-line tools, `setup-envtest`, Make, GitHub Actions YAML, `shellcheck`, `actionlint`.

## Global constraints

- `SKIP_INSTALLED=false` requires installation.
- `SKIP_INSTALLED=true` skips only an executable that passes its configured version check.
- Tools without a safe, meaningful `version` or `--version` command remain presence-only.
- `should-skip` accepts only `<command>` or `<command> <version|--version> <expected-text> [version-arg]`.
- `go-install` accepts only `<app> <reference>` or `<app> <version|--version> <expected-text> <reference>`.
- Any failed envtest check refreshes and relinks all three envtest binaries.
- `setup-envtest` is downloaded on every `kubebuilder` invocation.
- Kubernetes 1.25 special handling is removed; the supported CI matrix is Kubernetes 1.31-1.37.
- Do not add a separate envtest setup script.

---

## File map

- Create `hack/toolchain_test.sh`: focused, hermetic regression tests for helper policy, Go-tool dispatch, and envtest bundle repair.
- Modify `hack/toolchain.sh`: helper contracts, version-aware tool declarations, Ginkgo pinning, envtest bundle validation/repair, Trivy reuse, and source-safe `main` guard.
- Modify `Makefile`: run the focused shell regression test from `make verify`.
- Modify `.github/actions/install-deps/action.yaml`: run `make toolchain` on cache hits and misses with explicit skip/force policy.
- Modify `.github/workflows/ci.yml`: remove obsolete `dl.k8s.io` egress allowances.
- Modify `.github/workflows/ci-test.yml`: remove obsolete `dl.k8s.io` egress allowances.

### Task 1: Build and test the version-aware skip contract

**Files:**
- Create: `hack/toolchain_test.sh`
- Modify: `hack/toolchain.sh:4-56`

**Interfaces:**
- Produces: `should-skip <command> [version|--version <expected-text> [version-arg]]`
- Produces: `_command_is_current <command> [version|--version <expected-text> [version-arg]]`
- Produces: `go-install <app> [version|--version <expected-text>] <reference>`
- Return status `0`: skip/current.
- Return status `1`: installation required/not current.
- Return status `2`: invalid helper invocation.

- [ ] **Step 1: Add the shell test harness and failing helper tests**

First replace the unconditional `main "$@"` at the end of `hack/toolchain.sh` with this
testability guard, so sourcing the subject cannot install anything:

```bash
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
```

Then create `hack/toolchain_test.sh` with a temporary workspace, simple assertions, executable
stubs, and tests for policy precedence and the optional version argument:

```bash
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

    make_version_tool "${tool}" "Version: v0.19.0" 1
    SKIP_INSTALLED=true \
        assert_status 1 should-skip "${tool}" --version v0.19.0

    SKIP_INSTALLED=true \
        assert_status 2 should-skip "${tool}" -version v0.19.0
    SKIP_INSTALLED=true \
        assert_status 2 should-skip "${tool}" --version
}

test_should_skip_policy_and_versions
echo "PASS: toolchain helper tests"
```

- [ ] **Step 2: Run the helper test and confirm the baseline failure**

Run:

```bash
bash hack/toolchain_test.sh
```

Expected: non-zero exit because sourcing `hack/toolchain.sh` runs `main`, or because the existing
one-argument `should-skip` cannot satisfy the new version-aware assertions.

- [ ] **Step 3: Make `toolchain.sh` source-safe and implement the helper contract**

At the top of `hack/toolchain.sh`, retain test-overridable destinations:

```bash
K8S_VERSION="${K8S_VERSION:-1.34.x}"
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-/usr/local/kubebuilder/bin}"
SKIP_INSTALLED="${SKIP_INSTALLED:-false}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "${SCRIPT_DIR}")"
TOOL_DEST="${TOOL_DEST:-$(go env GOPATH)/bin}"
```

Replace the existing `should-skip` and `go-install` functions with:

```bash
_validate_version_check_args() {
    if [[ "$#" -eq 1 ]]; then
        return
    fi
    if [[ "$#" -ne 3 && "$#" -ne 4 ]]; then
        echo "[ERR] usage: should-skip <command> [version|--version <expected-text> [version-arg]]" >&2
        return 2
    fi
    case "$2" in
        version | --version) ;;
        *)
            echo "[ERR] unsupported version command '$2'; expected version or --version" >&2
            return 2
            ;;
    esac
}

_resolve_command() {
    local command_name="$1"
    if [[ "${command_name}" == */* ]]; then
        [[ -x "${command_name}" ]] || return 1
        printf '%s\n' "${command_name}"
        return
    fi
    command -v "${command_name}" 2>/dev/null
}

_command_is_current() {
    local validation_status=0
    _validate_version_check_args "$@" || validation_status=$?
    if [[ "${validation_status}" -ne 0 ]]; then
        return "${validation_status}"
    fi

    local command_path
    command_path="$(_resolve_command "$1")" || return 1
    if [[ "$#" -eq 1 ]]; then
        return
    fi

    local output
    if [[ "$#" -eq 4 ]]; then
        output="$("${command_path}" "$2" "$4" 2>&1)" || return 1
    else
        output="$("${command_path}" "$2" 2>&1)" || return 1
    fi
    grep -Fq -- "$3" <<<"${output}"
}

should-skip() {
    local validation_status=0
    _validate_version_check_args "$@" || validation_status=$?
    if [[ "${validation_status}" -ne 0 ]]; then
        return "${validation_status}"
    fi

    if [[ "${SKIP_INSTALLED}" != true ]]; then
        echo "[INF] Installing $1 because SKIP_INSTALLED is not true"
        return 1
    fi
    if _command_is_current "$@"; then
        return
    fi

    if [[ "$#" -eq 1 ]]; then
        echo "[INF] Installing $1 because it is missing"
    else
        echo "[INF] Installing $1 because the installed version does not contain '$3'"
    fi
    return 1
}

go-install() {
    local app
    local reference
    local -a check
    case "$#" in
        2)
            app="$1"
            reference="$2"
            check=("${TOOL_DEST}/${app}")
            ;;
        4)
            app="$1"
            reference="$4"
            check=("${TOOL_DEST}/${app}" "$2" "$3")
            ;;
        *)
            echo "[ERR] usage: go-install <app> [version|--version <expected-text>] <reference>" >&2
            return 2
            ;;
    esac

    local skip_status=0
    if should-skip "${check[@]}"; then
        return
    else
        skip_status=$?
    fi
    if [[ "${skip_status}" -ne 1 ]]; then
        return "${skip_status}"
    fi

    echo "[INF] Installing ${app}"
    go install "${reference}"
}
```

Remove the duplicated `TOOL_DEST` assignment. Keep the `BASH_SOURCE` guard added in Step 1.

- [ ] **Step 4: Run the helper test and shellcheck**

Run:

```bash
bash hack/toolchain_test.sh
pre-commit run shellcheck --files hack/toolchain.sh hack/toolchain_test.sh
```

Expected: both commands exit `0`; the test ends with `PASS: toolchain helper tests`.

- [ ] **Step 5: Commit the helper contract**

```bash
git add hack/toolchain.sh hack/toolchain_test.sh
git commit -m "feat: add version-aware toolchain skip checks" \
  -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 2: Apply version checks to managed tools

**Files:**
- Modify: `hack/toolchain.sh:65-95`
- Modify: `hack/toolchain.sh:119-129`
- Modify: `hack/toolchain_test.sh`

**Interfaces:**
- Consumes: `go-install` and `should-skip` from Task 1.
- Produces: `_ginkgo_version`, which prints the non-empty `v`-prefixed module version from `go.mod`.
- Preserves: presence-only installation for tools without safe version output.

- [ ] **Step 1: Add failing tests for both `go-install` forms and Ginkgo version resolution**

Before sourcing the subject in `hack/toolchain_test.sh`, prepend a fake `go` command directory to
`PATH`. The fake delegates `go env GOPATH`, records `go install`, and returns the repository's
Ginkgo version for `go -C ... list`:

```bash
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
```

Add:

```bash
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
```

Invoke both tests before the final PASS message.

- [ ] **Step 2: Run the focused test and confirm the missing Ginkgo helper**

Run:

```bash
bash hack/toolchain_test.sh
```

Expected: non-zero exit with `_ginkgo_version: command not found`.

- [ ] **Step 3: Migrate tool declarations and Trivy to version-aware checks**

Add:

```bash
_ginkgo_version() {
    local version
    version="$(GOWORK=off go -C "${REPO_ROOT}" list -m -f '{{ .Version }}' github.com/onsi/ginkgo/v2)" ||
        return 1
    if [[ -z "${version}" || "${version}" != v* ]]; then
        echo "[ERR] invalid Ginkgo module version '${version:-<empty>}'" >&2
        return 1
    fi
    printf '%s\n' "${version}"
}
```

Replace the `tools` body with version-aware calls where supported:

```bash
tools() {
    local ginkgo_version
    ginkgo_version="$(_ginkgo_version)"

    go-install go-licenses github.com/google/go-licenses/v2@3e084b0caf710f7bfead967567539214f598c0a2 # v2.0.1
    go-install ko version v0.17.1 github.com/google/ko@v0.17.1
    go-install yq --version v4.45.1 github.com/mikefarah/yq/v4@v4.45.1
    go-install helm-docs github.com/norwoodj/helm-docs/cmd/helm-docs@v1.14.2
    go-install controller-gen --version v0.19.0 sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
    go-install cosign version v2.4.1 github.com/sigstore/cosign/v2/cmd/cosign@v2.4.1
    # go install -tags extended github.com/gohugoio/hugo@v0.110.0
    go-install govulncheck --version v1.1.4 golang.org/x/vuln/cmd/govulncheck@v1.1.4
    go-install ginkgo version "${ginkgo_version#v}" "github.com/onsi/ginkgo/v2/ginkgo@${ginkgo_version}"
    go-install actionlint --version v1.7.7 github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
    go-install goveralls github.com/mattn/goveralls@v0.0.12
    go-install crane version v0.20.2 github.com/google/go-containerregistry/cmd/crane@v0.20.2
    go-install swagger version v0.33.1 github.com/go-swagger/go-swagger/cmd/swagger@v0.33.1
    go-install aks-node-viewer github.com/Azure/aks-node-viewer/cmd/aks-node-viewer@latest
    go-install pprof github.com/google/pprof@latest

    if ! echo "$PATH" | grep -q "${GOPATH:-undefined}/bin\|$HOME/go/bin"; then
        echo "Go workspace's \"bin\" directory is not in PATH. Run 'export PATH=\"\$PATH:\${GOPATH:-\$HOME/go}/bin\"'."
    fi

    go-install golangci-lint --version 2.12.2 github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

    local custom_status=0
    if should-skip "${TOOL_DEST}/golangci-lint-custom" --version v2.12.2-custom; then
        return
    else
        custom_status=$?
    fi
    if [[ "${custom_status}" -ne 1 ]]; then
        return "${custom_status}"
    fi

    echo "[INF] Installing golangci-lint custom modules"
    TOOL_DEST="${TOOL_DEST}" envsubst <"${SCRIPT_DIR}/custom-gcl.template.yml" >.custom-gcl.yml
    "${TOOL_DEST}/golangci-lint" custom -v
    rm .custom-gcl.yml
}
```

Refactor `gettrivy` to use the same policy:

```bash
gettrivy() {
    local version="0.74.0"
    local sha256="cf1e32ec8d4d8823e023096a28cadb14f5b5123ce03f201fb633c5b76aa712dd"
    local skip_status=0
    if should-skip trivy --version "${version}"; then
        return
    else
        skip_status=$?
    fi
    if [[ "${skip_status}" -ne 1 ]]; then
        return "${skip_status}"
    fi

    wget -qO /tmp/trivy.deb \
        "https://github.com/aquasecurity/trivy/releases/download/v${version}/trivy_${version}_Linux-64bit.deb"
    echo "${sha256}  /tmp/trivy.deb" | sha256sum --check --strict
    sudo dpkg -i /tmp/trivy.deb
    rm /tmp/trivy.deb
}
```

- [ ] **Step 4: Run focused tests and shellcheck**

Run:

```bash
bash hack/toolchain_test.sh
pre-commit run shellcheck --files hack/toolchain.sh hack/toolchain_test.sh
```

Expected: both commands exit `0`.

- [ ] **Step 5: Commit version-aware tool declarations**

```bash
git add hack/toolchain.sh hack/toolchain_test.sh
git commit -m "chore: validate installed tool versions" \
  -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 3: Repair envtest as one validated bundle

**Files:**
- Modify: `hack/toolchain.sh:97-117`
- Modify: `hack/toolchain_test.sh`

**Interfaces:**
- Consumes: `should-skip` for policy-aware pre-install checks.
- Consumes: `_command_is_current` for policy-free post-install verification.
- Produces: `_envtest_assets_are_current`, `_verify_envtest_assets`, `_install_setup_envtest`, `_resolve_envtest_assets`, `_link_envtest_assets`, and `_ensure_envtest_assets`.
- Guarantees: a successful `kubebuilder` return leaves executable, version-matched top-level `kube-apiserver` and `kubectl` links plus an executable `etcd` link.

- [ ] **Step 1: Add failing envtest bundle tests**

Add helpers to `hack/toolchain_test.sh`:

```bash
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
```

Add fake `curl` and `sudo` commands so the complete `kubebuilder` path remains inside `TEST_ROOT`:

```bash
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
exec "$@"
EOF
chmod +x "${FAKE_BIN}/sudo"
```

Add a test where one stale binary refreshes all links:

```bash
test_envtest_refreshes_the_complete_bundle() {
    local root="${TEST_ROOT}/envtest-refresh"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    local calls="${root}/setup-envtest-calls.log"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.34.1"
    make_asset_dir "${resolved}" "1.32.4"
    make_version_tool "${assets}/kubectl" "Client Version: v1.31.9"
    make_setup_envtest "${setup_envtest}" "${resolved}"

    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${setup_envtest}"
    SETUP_ENVTEST_CALL_LOG="${calls}"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG

    _ensure_envtest_assets
    [[ "$(readlink "${assets}/kube-apiserver")" == "${resolved}/kube-apiserver" ]] ||
        fail "expected kube-apiserver link to refresh"
    [[ "$(readlink "${assets}/kubectl")" == "${resolved}/kubectl" ]] ||
        fail "expected kubectl link to refresh"
    [[ "$(readlink "${assets}/etcd")" == "${resolved}/etcd" ]] ||
        fail "expected etcd link to refresh"
    assert_contains "${calls}" "use --force -p path 1.32.x"
}
```

Add the healthy-bundle and kubectl-argument case:

```bash
test_envtest_keeps_a_healthy_bundle() {
    local root="${TEST_ROOT}/envtest-healthy"
    local assets="${root}/assets"
    local resolved="${root}/resolved"
    local setup_envtest="${root}/setup-envtest"
    local calls="${root}/setup-envtest-calls.log"
    mkdir -p "${assets}" "${resolved}"
    make_asset_dir "${assets}" "1.32.4"
    make_asset_dir "${resolved}" "1.32.5"
    make_setup_envtest "${setup_envtest}" "${resolved}"

    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${setup_envtest}"
    SETUP_ENVTEST_CALL_LOG="${calls}"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG

    _ensure_envtest_assets
    [[ ! -e "${calls}" || ! -s "${calls}" ]] ||
        fail "healthy assets should not call setup-envtest use"
    assert_contains "${VERSION_ARGS_LOG}" "version --client=true"
}
```

Add the missing-member all-or-nothing case:

```bash
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

    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${setup_envtest}"
    SETUP_ENVTEST_CALL_LOG="${root}/calls.log"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG

    _ensure_envtest_assets
    for binary in kube-apiserver kubectl etcd; do
        [[ "$(readlink "${assets}/${binary}")" == "${resolved}/${binary}" ]] ||
            fail "expected ${binary} to refresh with the bundle"
    done
}
```

Add explicit incomplete- and wrong-version failure cases:

```bash
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

    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${setup_envtest}"
    SETUP_ENVTEST_CALL_LOG="${root}/calls.log"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG

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

    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${setup_envtest}"
    SETUP_ENVTEST_CALL_LOG="${root}/calls.log"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG

    assert_status 1 _ensure_envtest_assets
}
```

Finally, add one complete `kubebuilder` test that proves the pinned helper is downloaded even when
the bundle is healthy:

```bash
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

    K8S_VERSION=1.32.x
    KUBEBUILDER_ASSETS="${assets}"
    SETUP_ENVTEST_BIN="${installed}"
    SETUP_ENVTEST_FIXTURE="${fixture}"
    SETUP_ENVTEST_CALL_LOG="${root}/calls.log"
    VERSION_ARGS_LOG="${root}/version-args.log"
    SKIP_INSTALLED=true
    export K8S_VERSION KUBEBUILDER_ASSETS SETUP_ENVTEST_BIN SETUP_ENVTEST_FIXTURE
    export SETUP_ENVTEST_CALL_LOG VERSION_ARGS_LOG

    kubebuilder
    [[ -x "${installed}" ]] || fail "expected setup-envtest to be downloaded"
    [[ ! -e "${SETUP_ENVTEST_CALL_LOG}" || ! -s "${SETUP_ENVTEST_CALL_LOG}" ]] ||
        fail "healthy assets should not be resolved again"
}
```

Also add and invoke:

- `test_directory_shaped_etcd_refreshes_every_link`, which replaces `assets/etcd` with a directory
  and requires all three links to move to the resolved bundle.
- `test_mixed_envtest_bundle_refreshes_every_link`, which points the Kubernetes binaries and
  `etcd` at different canonical directories and requires one-bundle refresh.
- `test_setup_envtest_digest_mismatch_preserves_existing_helper`, which supplies an invalid
  `SETUP_ENVTEST_SHA256`, expects `_install_setup_envtest` to fail, and executes the existing
  helper to prove it was not replaced.

Invoke all envtest tests before the final PASS message.

Call these tests before the final PASS message.

- [ ] **Step 2: Run the focused test and confirm envtest helpers are absent**

Run:

```bash
bash hack/toolchain_test.sh
```

Expected: non-zero exit with `_resolve_envtest_assets: command not found`.

- [ ] **Step 3: Add testable envtest defaults and helper functions**

Add these globals near the other defaults:

```bash
SETUP_ENVTEST_BIN="${SETUP_ENVTEST_BIN:-${KUBEBUILDER_ASSETS}/setup-envtest}"
SETUP_ENVTEST_VERSION="v0.22.3"
SETUP_ENVTEST_URL="${SETUP_ENVTEST_URL:-}"
SETUP_ENVTEST_SHA256="${SETUP_ENVTEST_SHA256:-}"
```

Implement:

```bash
_expected_kubernetes_version() {
    printf 'v%s\n' "${K8S_VERSION%.x}"
}

_envtest_assets_share_directory() {
    local binary
    local common_directory=""
    local resolved
    local resolved_directory
    for binary in kube-apiserver kubectl etcd; do
        resolved="$(realpath "${KUBEBUILDER_ASSETS}/${binary}")" || return 1
        resolved_directory="$(dirname "${resolved}")"
        if [[ -z "${common_directory}" ]]; then
            common_directory="${resolved_directory}"
        elif [[ "${resolved_directory}" != "${common_directory}" ]]; then
            return 1
        fi
    done
}

_envtest_assets_are_current() {
    local expected
    expected="$(_expected_kubernetes_version)"
    should-skip "${KUBEBUILDER_ASSETS}/kube-apiserver" --version "${expected}" &&
        should-skip "${KUBEBUILDER_ASSETS}/kubectl" version "${expected}" --client=true &&
        should-skip "${KUBEBUILDER_ASSETS}/etcd" &&
        _envtest_assets_share_directory
}

_verify_envtest_assets() {
    local expected
    expected="$(_expected_kubernetes_version)"
    _command_is_current "${KUBEBUILDER_ASSETS}/kube-apiserver" --version "${expected}" &&
        _command_is_current "${KUBEBUILDER_ASSETS}/kubectl" version "${expected}" --client=true &&
        _command_is_current "${KUBEBUILDER_ASSETS}/etcd" &&
        _envtest_assets_share_directory
}

_ensure_directory() {
    local path="$1"
    if ! mkdir -p "${path}" 2>/dev/null; then
        sudo mkdir -p "${path}"
    fi
}

_ensure_writable_directory() {
    local path="$1"
    _ensure_directory "${path}"
    if [[ ! -w "${path}" ]]; then
        sudo chown "${USER}" "${path}"
    fi
}

_setup_envtest_sha256() {
    case "$1-$2" in
        darwin-amd64) printf '%s\n' "390aad0f8fce155b0df483775bebd813ac0bd1dc11ee458147f3ae4d1e2178b9" ;;
        darwin-arm64) printf '%s\n' "415b69c6bebad2353045eccc376b9407058aa38a3e9720b5af0232af396e62f7" ;;
        linux-amd64) printf '%s\n' "a1776d9b6266a05d1b18fc13a0788a3d2a4a44265f19eb81f5d80223ccb6262f" ;;
        linux-arm64) printf '%s\n' "f773d4c9191101c7bd714e0ebf1e0fa4a98a6548e098554ee1db379d5e84b0bb" ;;
        linux-ppc64le) printf '%s\n' "e50ed61d2776f62db4c8f148cd75c4750c6c928adc32426a9b65d2c1201b0064" ;;
        linux-s390x) printf '%s\n' "39222e69f6ca7313f21a17ba55607980da23af98fcb0ecb4c56b1bc7d3d66257" ;;
        *)
            echo "[ERR] no setup-envtest checksum for $1/$2" >&2
            return 1
            ;;
    esac
}

_install_setup_envtest() {
    local arch
    local os
    local url
    local download
    local destination_dir
    local expected_sha256
    arch="$(go env GOARCH)"
    os="$(go env GOOS)"
    url="${SETUP_ENVTEST_URL:-https://github.com/kubernetes-sigs/controller-runtime/releases/download/${SETUP_ENVTEST_VERSION}/setup-envtest-${os}-${arch}}"
    expected_sha256="${SETUP_ENVTEST_SHA256:-$(_setup_envtest_sha256 "${os}" "${arch}")}"
    download="$(mktemp)"
    destination_dir="$(dirname "${SETUP_ENVTEST_BIN}")"

    if ! curl -fsSL "${url}" --output "${download}"; then
        rm -f "${download}"
        return 1
    fi
    if ! printf '%s  %s\n' "${expected_sha256}" "${download}" | sha256sum --check --strict; then
        rm -f "${download}"
        return 1
    fi
    _ensure_directory "${destination_dir}"
    local install_status=0
    if [[ -w "${destination_dir}" ]]; then
        install -m 0755 "${download}" "${SETUP_ENVTEST_BIN}" || install_status=$?
    else
        sudo install -m 0755 "${download}" "${SETUP_ENVTEST_BIN}" || install_status=$?
    fi
    if [[ "${install_status}" -ne 0 ]]; then
        rm -f "${download}"
        return "${install_status}"
    fi
    rm -f "${download}"
}

_link_envtest_assets() {
    local asset_dir="$1"
    local binary
    if [[ -z "${asset_dir}" || "${asset_dir}" != /* || "${asset_dir}" == "/" || ! -d "${asset_dir}" ]]; then
        echo "[ERR] setup-envtest returned invalid asset directory '${asset_dir:-<empty>}'" >&2
        return 1
    fi
    if [[ -z "${KUBEBUILDER_ASSETS}" || "${KUBEBUILDER_ASSETS}" == "/" ]]; then
        echo "[ERR] invalid kubebuilder asset destination '${KUBEBUILDER_ASSETS:-<empty>}'" >&2
        return 1
    fi

    for binary in kube-apiserver kubectl etcd; do
        if [[ ! -f "${asset_dir}/${binary}" || ! -x "${asset_dir}/${binary}" ]]; then
            echo "[ERR] envtest asset is missing or not executable: ${asset_dir}/${binary}" >&2
            return 1
        fi
    done
    for binary in kube-apiserver kubectl etcd; do
        local destination="${KUBEBUILDER_ASSETS}/${binary}"
        if [[ -d "${destination}" && ! -L "${destination}" ]]; then
            rm -rf -- "${destination}"
        else
            rm -f -- "${destination}"
        fi
        ln -s "${asset_dir}/${binary}" "${destination}"
    done
}

_resolve_envtest_assets() {
    local arch
    local asset_dir
    arch="$(go env GOARCH)"
    asset_dir="$("${SETUP_ENVTEST_BIN}" use --force -p path "${K8S_VERSION}" \
        --arch="${arch}" \
        --bin-dir="${KUBEBUILDER_ASSETS}")"
    _link_envtest_assets "${asset_dir}"
}

_ensure_envtest_assets() {
    if _envtest_assets_are_current; then
        return
    fi

    echo "[INF] Refreshing the complete envtest bundle for Kubernetes ${K8S_VERSION}"
    _resolve_envtest_assets
    if ! _verify_envtest_assets; then
        echo "[ERR] envtest bundle does not match Kubernetes ${K8S_VERSION}" >&2
        return 1
    fi
}
```

- [ ] **Step 4: Replace `kubebuilder` and remove Kubernetes 1.25 handling**

Replace the existing function, including the 1.25.16 block, with:

```bash
kubebuilder() {
    echo "[INF] Setting up kubebuilder binaries for Kubernetes ${K8S_VERSION}"
    _ensure_writable_directory "${KUBEBUILDER_ASSETS}"
    _install_setup_envtest
    _ensure_envtest_assets
}
```

Use the fake `curl` and `sudo` commands from Step 1 for the complete `kubebuilder` test. The other
envtest cases call `_ensure_envtest_assets` directly, so they exercise bundle decisions without
performing a helper download.

- [ ] **Step 5: Run envtest tests and shellcheck**

Run:

```bash
bash hack/toolchain_test.sh
pre-commit run shellcheck --files hack/toolchain.sh hack/toolchain_test.sh
```

Expected: both commands exit `0`; no test writes outside its temporary directory.

- [ ] **Step 6: Commit envtest bundle healing**

```bash
git add hack/toolchain.sh hack/toolchain_test.sh
git commit -m "fix: heal stale envtest toolchain bundles" \
  -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 4: Run validation on every cache outcome

**Files:**
- Modify: `Makefile:94-95`
- Modify: `.github/actions/install-deps/action.yaml:25-33`
- Modify: `.github/workflows/ci.yml:18-21`
- Modify: `.github/workflows/ci-test.yml:27-30`

**Interfaces:**
- Consumes: version-aware `make toolchain` behaviour from Tasks 1-3.
- Produces: one CI action path for both cache hits and cache misses.
- Produces: `make verify` coverage for `hack/toolchain_test.sh`.

- [ ] **Step 1: Wire the focused regression test into `make verify`**

Add the test before the real toolchain installation:

```make
verify: tidy download ## Verify code. Includes dependencies, linting, formatting, etc
	bash hack/toolchain_test.sh
	SKIP_INSTALLED=true make toolchain
```

- [ ] **Step 2: Replace the cache-miss-only action step**

In `.github/actions/install-deps/action.yaml`, replace:

```yaml
    - if: ${{ steps.cache-toolchain.outputs.cache-hit != 'true' }}
      shell: bash
      env:
        K8S_VERSION: ${{ inputs.k8sVersion }}
      run: make toolchain
```

with:

```yaml
    - shell: bash
      env:
        K8S_VERSION: ${{ inputs.k8sVersion }}
        SKIP_INSTALLED: ${{ steps.cache-toolchain.outputs.cache-hit == 'true' }}
      run: make toolchain
```

- [ ] **Step 3: Remove obsolete Kubernetes 1.25 egress allowances**

In both `.github/workflows/ci.yml` and `.github/workflows/ci-test.yml`, change the
`allowed-endpoints` header to:

```yaml
        allowed-endpoints: >
```

Remove these two entries from both files:

```yaml
          *.dl.k8s.io:443
          dl.k8s.io:443
```

- [ ] **Step 4: Run focused validation**

Run:

```bash
bash hack/toolchain_test.sh
actionlint -oneline
pre-commit run shellcheck --files hack/toolchain.sh hack/toolchain_test.sh
```

Expected: all three commands exit `0`.

- [ ] **Step 5: Commit CI integration**

```bash
git add Makefile .github/actions/install-deps/action.yaml \
  .github/workflows/ci.yml .github/workflows/ci-test.yml
git commit -m "ci: validate cached toolchain versions" \
  -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

### Task 5: Verify the complete change

**Files:**
- Verify: `hack/toolchain.sh`
- Verify: `hack/toolchain_test.sh`
- Verify: `Makefile`
- Verify: `.github/actions/install-deps/action.yaml`
- Verify: `.github/workflows/ci.yml`
- Verify: `.github/workflows/ci-test.yml`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: fresh evidence that the focused tests, repository verification, and clean-tree check pass together.

- [ ] **Step 1: Run the focused shell suite independently**

Run:

```bash
bash hack/toolchain_test.sh
```

Expected: exit `0` and `PASS: toolchain helper tests`.

- [ ] **Step 2: Run repository verification**

Run:

```bash
make verify
```

Expected: exit `0`, including the new shell test, toolchain version checks, code generation,
linters, and `actionlint`.

- [ ] **Step 3: Confirm verification left no generated or accidental changes**

Run:

```bash
git --no-pager status --short
```

Expected: no output.

- [ ] **Step 4: Review the branch commits**

Run:

```bash
git --no-pager log --oneline origin/main..HEAD
```

Expected: the approved design commit followed by focused helper, tool-version, envtest-healing, and
CI-integration commits. If implementation required a correction after `make verify`, commit only
that correction with the standard Copilot co-author trailer before repeating Steps 1-3.
