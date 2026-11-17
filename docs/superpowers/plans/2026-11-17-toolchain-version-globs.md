# Toolchain Version Globs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Accept caller-supplied version globs so envtest patch versions and custom golangci-lint build suffixes match correctly.

**Architecture:** Match each whitespace-delimited command-output token against the caller's Bash glob. Convert wildcard Kubernetes selectors in the existing shared helper and broaden only the custom linter's caller-supplied pattern.

**Tech Stack:** Bash, tr, existing toolchain script.

## Global Constraints

- Keep changes minimal and confined to version matching, its two affected callers, and directly related comments.
- Patterns without wildcard syntax retain exact matching.
- Explicit wildcard patterns intentionally permit suffixes, including prerelease suffixes.
- Callers must quote patterns containing wildcard characters.
- Preserve argument validation, command resolution, return codes, logging, CI abort behaviour, and local installation behaviour.
- Both envtest pre-install and post-install checks use the existing shared expected-version helper.
- Do not install tools or create Azure resources for the focused regression checks.
- Never bypass the configured Go proxy.
- Approved spec: `docs/superpowers/specs/2026-11-17-toolchain-version-globs-design.md`.

---

## File map

- Modify `hack/toolchain.sh`: existing matching function, Kubernetes selector helper, custom linter pattern, and associated comments.
- Create `/home/bevan/.copilot/session-state/9b8845c6-1fd0-45dd-a10b-0af53eb36878/files/toolchain-version-globs-test.sh`: local regression harness, outside the repository. No existing dedicated toolchain test suite was found.
- No dependencies or installation paths change.

### Task 1: Implement and verify caller-supplied version globs

**Interfaces:**
- Consumes: `_command_is_current <command> [version|--version <pattern> [version-arg]]`, `should-skip` with the same arguments, and `_expected_kubernetes_version` using `K8S_VERSION`.
- Produces: the same function signatures; status 0 for a matching token, 1 for an unavailable executable, 2 for invalid arguments or a failed version command, and 3 for no matching token.
- `go-install <app> <version-command> <pattern> <reference>` continues to pass its third argument unchanged.

- [x] **Step 1: Create a focused regression harness**

Use `apply_patch` to create the session-local file listed above with this content:

```bash
#!/usr/bin/env bash
set -euo pipefail

# The harness doubles as a version-reporting executable.
if [[ "${1:-}" == --version || "${1:-}" == version ]]; then
    printf '%s\n' "${VERSION_OUTPUT:-}"
    exit "${VERSION_EXIT:-0}"
fi

ROOT=/home/bevan/github/karpenter-provider-azure-worktrees/theunrepentantgeek-toolchain-version-healing
SELF="$(realpath "${BASH_SOURCE[0]}")"
export TOOL_DEST="${SELF%/*}"
source "${ROOT}/hack/toolchain.sh"
export VERSION_EXIT=0
SKIP_INSTALLED=true
CI=false
failures=0

expect_status() {
    local expected="$1" actual=0
    shift
    "$@" || actual=$?
    if [[ "${actual}" -ne "${expected}" ]]; then
        printf 'FAIL: expected %s, got %s: %s\n' \
            "${expected}" "${actual}" "$*" >&2
        failures=$((failures + 1))
    fi
}

for version in v1.34.1 v1.34.99 v1.34.1-rc.1; do
    export VERSION_OUTPUT="Kubernetes ${version}"
    expect_status 0 _command_is_current "${SELF}" --version 'v1.34.*'
done
for version in v1.33.2 v1.340.1; do
    export VERSION_OUTPUT="Kubernetes ${version}"
    expect_status 3 _command_is_current "${SELF}" --version 'v1.34.*'
done
export VERSION_OUTPUT='Kubernetes v1.33.2'
expect_status 0 _command_is_current "${SELF}" --version 'v1.33.*'
export VERSION_OUTPUT='Kubernetes v1.34.1'
expect_status 0 _command_is_current "${SELF}" --version v1.34.1
export VERSION_OUTPUT='Kubernetes v1.34.2'
expect_status 3 _command_is_current "${SELF}" --version v1.34.1

for version in \
    v2.14.0 \
    v2.14.0-custom-gcl-47DEQpj8HBSaTImW5JCeuQeRkm5NMpJWZG3hSuFU \
    v2.14.0-rc.1; do
    export VERSION_OUTPUT="golangci-lint has version ${version}"
    expect_status 0 _command_is_current "${SELF}" --version 'v2.14.0*'
done
export VERSION_OUTPUT='golangci-lint has version v2.15.0-custom-gcl-hash'
expect_status 3 _command_is_current "${SELF}" --version 'v2.14.0*'

export VERSION_OUTPUT=$'controller-gen\tv0.19.0\nbuild information'
expect_status 0 _command_is_current "${SELF}" --version v0.19.0
expect_status 0 should-skip "${SELF}" --version v0.19.0
for version in v0.19.0-rc.1 v0.19.01 prefix-v0.19.0; do
    export VERSION_OUTPUT="controller-gen ${version}"
    expect_status 3 _command_is_current "${SELF}" --version v0.19.0
done
expect_status 1 should-skip "${SELF}" --version v0.19.0

expect_status 0 _command_is_current "${SELF}"
expect_status 1 _command_is_current "${SELF}.missing"
export VERSION_EXIT=1
expect_status 2 _command_is_current "${SELF}" --version v0.19.0
export VERSION_EXIT=0
expect_status 2 _command_is_current "${SELF}" unsupported v0.19.0

for pair in '1.34.x v1.34.*' '1.33.x v1.33.*' '1.34.1 v1.34.1'; do
    read -r K8S_VERSION expected <<<"${pair}"
    actual="$(_expected_kubernetes_version)"
    if [[ "${actual}" != "${expected}" ]]; then
        printf 'FAIL: selector %s: expected %s, got %s\n' \
            "${K8S_VERSION}" "${expected}" "${actual}" >&2
        failures=$((failures + 1))
    fi
done

if [[ "${failures}" -ne 0 ]]; then
    printf 'FAIL: %s regression checks\n' "${failures}" >&2
    exit 1
fi
printf 'PASS: version glob regression checks\n'
```

- [x] **Step 2: Run the harness before implementation**

```bash
chmod +x /home/bevan/.copilot/session-state/9b8845c6-1fd0-45dd-a10b-0af53eb36878/files/toolchain-version-globs-test.sh
bash /home/bevan/.copilot/session-state/9b8845c6-1fd0-45dd-a10b-0af53eb36878/files/toolchain-version-globs-test.sh
```

Expected: nonzero exit; wildcard matching and `.x` selector conversion fail. Exact-token cases retain their current results.

- [x] **Step 3: Make the surgical script changes**

In `_command_is_current`, replace the matching comment and `tr | grep` pipeline with:

```bash
    # Match whole whitespace-delimited tokens against the caller's glob.
    local token
    while IFS= read -r token; do
        # The caller supplies a glob pattern, not a literal version.
        # shellcheck disable=SC2053
        if [[ "${token}" == $3 ]]; then
            return 0
        fi
    done < <(tr '[:space:]' '\n' <<<"${output}")
    return 3
```

The unquoted right-hand operand of `[[ ... == ... ]]` intentionally enables
Bash pattern matching, not filesystem expansion. The loop stays in the
function's shell so `return` exits the function. ShellCheck flags the
intentional glob as SC2053; suppress only that comparison, with the
explanation shown above. Quoting `$3` would disable the required matching.

Replace `_expected_kubernetes_version` with:

```bash
_expected_kubernetes_version() {
    if [[ "${K8S_VERSION}" == *.x ]]; then
        printf 'v%s.*\n' "${K8S_VERSION%.x}"
    else
        printf 'v%s\n' "${K8S_VERSION}"
    fi
}
```

Replace the custom linter's `should-skip` line with:

```bash
    if should-skip "${TOOL_DEST}/golangci-lint-custom" --version 'v2.14.0*'; then
```

Replace the existing `go-install` documentation block with:

```bash
# go-install installs a Go tool unless should-skip confirms it is already suitable.
#
# Presence-only form:
#   go-install <app> <reference>
#
# Version-aware form:
#   go-install <app> <version|--version> <expected-pattern> <reference>
#
# <app> is the binary name under TOOL_DEST, <expected-pattern> is a Bash glob
# matched against whole whitespace-delimited version-output tokens, and
# <reference> is the package passed to go install. Quote wildcard patterns.
```

Replace the argument-validation usage text with:

```bash
        echo "[ERR] usage: should-skip <command> [version|--version <expected-pattern> [version-arg]]" >&2
```

Replace the `go-install` argument-error usage text with:

```bash
            echo "[ERR] usage: go-install <app> [version|--version <expected-pattern>] <reference>" >&2
```

Leave installation, status codes, CI behaviour, and other call sites unchanged.

- [x] **Step 4: Verify the exact requirement**

```bash
bash -n hack/toolchain.sh
shellcheck hack/toolchain.sh
bash /home/bevan/.copilot/session-state/9b8845c6-1fd0-45dd-a10b-0af53eb36878/files/toolchain-version-globs-test.sh
git diff --check
```

Expected: syntax and whitespace checks succeed; harness ends with
`PASS: version glob regression checks`. Its invalid-argument case
intentionally prints the usage error.

Confirm by inspecting only the changed call-site hunks that the custom
linter uses the quoted wildcard and both envtest paths still call the
shared selector helper.

- [x] **Step 5: Run repository-required verification**

```bash
make verify
```

Expected: success if the repository's required tools are present.
The repository documents that verification may reject a dirty working
tree; distinguish that from an actual validation failure. Do not discard
unrelated changes, install tools pre-emptively, or bypass the configured
Go proxy. Report concrete environment failures and any generated changes.
Do not broaden this into a whole-branch review.

Execution result: all 25 focused regression checks, Bash syntax, and
ShellCheck pass. The installed Kubernetes 1.34.1 bundle passes both envtest
checks; the installed 2.14.0 custom linter is skipped without rebuilding.
Full verification was attempted with the official Go proxy credentials
refreshed and `KUBEBUILDER_ASSETS=/usr/local/kubebuilder/bin`; it passes
envtest setup but stops when the unrelated Trivy installation requires
an interactive sudo password.

- [x] **Step 6: Commit the focused implementation and plan update**

```bash
git add -- hack/toolchain.sh docs/superpowers/plans/2026-11-17-toolchain-version-globs.md
git commit -m "fix: support glob patterns in toolchain version checks" \
    -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

Report the focused test outcome, verification outcome, and commit.
Remove only the named session-local regression harness after validation
unless it is needed to investigate a failure.
