#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(dirname "$(realpath "$0")")
REPO_ROOT=$(dirname "$SCRIPT_DIR")
TOOLCHAIN_SCRIPT="$SCRIPT_DIR/toolchain.sh"
GO_TOOL_PATH_SCRIPT="$SCRIPT_DIR/go-tool-path.sh"
ACTION_FILE="$REPO_ROOT/.github/actions/install-deps/action.yaml"
MAKEFILE="$REPO_ROOT/Makefile"
EXPECTED_GINKGO_VERSION="v9.8.7"
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

assert_call_count() {
    local expected="$1"
    local call="$2"
    local calls_file="$3"
    local actual
    actual=$(grep -Fxc -- "$call" "$calls_file" || true)
    [[ "$actual" == "$expected" ]] || fail "expected $expected calls to '$call', got $actual"
}

[[ -x "$GO_TOOL_PATH_SCRIPT" ]] || fail "missing executable $GO_TOOL_PATH_SCRIPT"

write_fake_go() {
    local path="$1"
    cat > "$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s' "$1" >> "$GO_CALLS"
printf ' %s' "${@:2}" >> "$GO_CALLS"
printf '\n' >> "$GO_CALLS"

if [[ "$#" == 2 && "$1" == "env" && "$2" == "GOBIN" ]]; then
    printf '%s\n' "$FAKE_GOBIN"
elif [[ "$#" == 2 && "$1" == "env" && "$2" == "GOPATH" ]]; then
    printf '%s\n' "$FAKE_GOPATH"
elif [[ "$#" == 7 && "$1" == "-C" && "$2" == "$EXPECTED_REPO_ROOT" && "$3" == "list" && "$4" == "-m" && "$5" == "-f" && "$6" == "{{ .Version }}" && "$7" == "github.com/onsi/ginkgo/v2" ]]; then
    [[ "${GOWORK:-}" == "off" ]] || exit 89
    case "$LOOKUP_MODE" in
        value) printf '%s\n' "$EXPECTED_GINKGO_VERSION" ;;
        value-fail) printf '%s\n' "$EXPECTED_GINKGO_VERSION"; exit 42 ;;
        empty) ;;
        fail) exit 42 ;;
        *) exit 90 ;;
    esac
elif [[ "$#" == 2 && "$1" == "install" && "$2" == "github.com/onsi/ginkgo/v2/ginkgo@$EXPECTED_GINKGO_VERSION" ]]; then
    :
else
    printf 'unexpected go command: %q ' "$@" >&2
    printf '\n' >&2
    exit 88
fi
EOF
    chmod +x "$path"
}

write_fake_ginkgo() {
    local path="$1"
    local installed_version="$2"
    local exit_code="${3:-0}"
    cat > "$path" <<EOF
#!/usr/bin/env bash
set -euo pipefail
[[ "\$#" == 1 && "\$1" == "version" ]] || exit 87
printf 'probe\n' >> "\$GINKGO_PROBES"
printf 'Ginkgo Version ${installed_version}\n'
exit ${exit_code}
EOF
    chmod +x "$path"
}

run_case() {
    local name="$1"
    local installed_version="$2"
    local expected_install="$3"
    local use_gobin="$4"
    local resolved_version="${5:-$EXPECTED_GINKGO_VERSION}"
    local case_dir="$TEMP_DIR/$name"
    local gopath_one="$case_dir/gopath-one"
    local gopath_two="$case_dir/gopath-two"
    local fake_gobin=""
    local tool_dir="$gopath_one/bin"
    local go_calls="$case_dir/go-calls"
    local ginkgo_probes="$case_dir/ginkgo-probes"

    if [[ "$use_gobin" == "yes" ]]; then
        fake_gobin="$case_dir/gobin"
        tool_dir="$fake_gobin"
    fi
    mkdir -p "$case_dir/bin" "$tool_dir" "$gopath_one/bin" "$gopath_two/bin"
    : > "$go_calls"
    : > "$ginkgo_probes"
    write_fake_go "$case_dir/bin/go"

    # A PATH-shadowing binary must never be probed or executed by the toolchain.
    cat > "$case_dir/bin/ginkgo" <<'EOF'
#!/usr/bin/env bash
exit 86
EOF
    chmod +x "$case_dir/bin/ginkgo"

    if [[ "$installed_version" == "broken" ]]; then
        write_fake_ginkgo "$tool_dir/ginkgo" "${resolved_version#v}" 1
    elif [[ "$installed_version" != "missing" ]]; then
        write_fake_ginkgo "$tool_dir/ginkgo" "$installed_version" 0
    fi
    if [[ "$use_gobin" == "yes" ]]; then
        write_fake_ginkgo "$gopath_one/bin/ginkgo" "0.0.1"
    fi

    cp "$GO_TOOL_PATH_SCRIPT" "$case_dir/go-tool-path.sh"
    grep -v '^main "\$@"$' "$TOOLCHAIN_SCRIPT" > "$case_dir/toolchain-library.sh"
    (
        export EXPECTED_GINKGO_VERSION="$resolved_version" EXPECTED_REPO_ROOT="$REPO_ROOT"
        export FAKE_GOBIN="$fake_gobin" FAKE_GOPATH="$gopath_one:$gopath_two"
        export GO_CALLS="$go_calls" GINKGO_PROBES="$ginkgo_probes" LOOKUP_MODE=value
        export PATH="$case_dir/bin:$PATH" SKIP_INSTALLED=true
        # shellcheck source=/dev/null
        source "$case_dir/toolchain-library.sh"
        [[ "$TOOL_DEST" == "$tool_dir" ]]
        install-ginkgo
    )

    local expected_calls="env GOBIN"
    if [[ "$use_gobin" != "yes" ]]; then
        expected_calls+=$'\n'"env GOPATH"
    fi
    expected_calls+=$'\n'"-C $REPO_ROOT list -m -f {{ .Version }} github.com/onsi/ginkgo/v2"
    if [[ "$expected_install" == "yes" ]]; then
        expected_calls+=$'\n'"install github.com/onsi/ginkgo/v2/ginkgo@$resolved_version"
    fi
    [[ "$(<"$go_calls")" == "$expected_calls" ]] || fail "$name produced an unexpected or out-of-order Go call transcript"
    if [[ "$installed_version" == "missing" ]]; then
        [[ ! -s "$ginkgo_probes" ]] || fail "$name unexpectedly probed a missing binary"
    else
        [[ "$(wc -l < "$ginkgo_probes")" == 1 ]] || fail "$name did not probe exactly once"
    fi

    local install_call="install github.com/onsi/ginkgo/v2/ginkgo@$resolved_version"
    if [[ "$expected_install" == "yes" ]]; then
        assert_call_count 1 "$install_call" "$go_calls"
    else
        assert_call_count 0 "$install_call" "$go_calls"
    fi
}

run_lookup_failure_case() {
    local mode="$1"
    local case_dir="$TEMP_DIR/lookup-$mode"
    local gopath="$case_dir/gopath"
    local go_calls="$case_dir/go-calls"
    mkdir -p "$case_dir/bin" "$gopath/bin"
    : > "$go_calls"
    write_fake_go "$case_dir/bin/go"
    cp "$GO_TOOL_PATH_SCRIPT" "$case_dir/go-tool-path.sh"
    grep -v '^main "\$@"$' "$TOOLCHAIN_SCRIPT" > "$case_dir/toolchain-library.sh"

    if (
        export EXPECTED_GINKGO_VERSION EXPECTED_REPO_ROOT="$REPO_ROOT"
        export FAKE_GOBIN="" FAKE_GOPATH="$gopath:$case_dir/other-gopath"
        export GO_CALLS="$go_calls" GINKGO_PROBES="$case_dir/probes" LOOKUP_MODE="$mode"
        export PATH="$case_dir/bin:$PATH" SKIP_INSTALLED=true
        # shellcheck source=/dev/null
        source "$case_dir/toolchain-library.sh"
        install-ginkgo
    ); then
        fail "$mode Ginkgo module lookup unexpectedly succeeded"
    fi
    if grep -Fq 'install github.com/onsi/ginkgo/v2/ginkgo@' "$go_calls"; then
        fail "$mode lookup attempted to install Ginkgo"
    fi
    local expected_calls=$'env GOBIN\nenv GOPATH\n'"-C $REPO_ROOT list -m -f {{ .Version }} github.com/onsi/ginkgo/v2"
    [[ "$(<"$go_calls")" == "$expected_calls" ]] || fail "$mode lookup produced an unexpected Go call transcript"
}

run_case matching 9.8.7 no no v9.8.7
run_case matching-alt 1.2.3 no no v1.2.3
run_case mismatched 9.8.8 yes no v9.8.7
run_case broken broken yes no v9.8.7
run_case missing missing yes no v9.8.7
run_case gobin-matching 9.8.7 no yes v9.8.7
run_lookup_failure_case empty
run_lookup_failure_case fail
run_lookup_failure_case value-fail

# Make must execute the same absolute Ginkgo path that the installer manages.
grep -Fq 'GINKGO := $(shell ./hack/go-tool-path.sh ginkgo)' "$MAKEFILE" || fail "Makefile must ignore ambient GINKGO values"
[[ "$(grep -Fc $'\t"$(GINKGO)"' "$MAKEFILE")" == 2 ]] || fail "Makefile must invoke the quoted \$(GINKGO) path exactly twice"
managed_ginkgo=$("$GO_TOOL_PATH_SCRIPT" ginkgo)
GINKGO=ginkgo make -s -C "$REPO_ROOT" --dry-run test | grep -Fq "\"$managed_ginkgo\" -vv" || fail "ambient GINKGO shadows the managed path"
make -s -C "$REPO_ROOT" GINKGO=/tmp/explicit-ginkgo --dry-run test | grep -Fq '"/tmp/explicit-ginkgo" -vv' || fail "explicit Make override no longer works"
if grep -Eq $'^\t+ginkgo([[:space:]]|$)' "$MAKEFILE"; then
    fail "Makefile still invokes a PATH-selected ginkgo binary"
fi

# Validate the composite action structurally, including producer-before-consumer ordering.
yq eval '.' "$ACTION_FILE" >/dev/null
tool_versions_count=$(yq eval '[.runs.steps[] | select(.id == "tool-versions")] | length' "$ACTION_FILE")
cache_count=$(yq eval '[.runs.steps[] | select(.id == "cache-toolchain")] | length' "$ACTION_FILE")
[[ "$tool_versions_count" == 1 && "$cache_count" == 1 ]] || fail "expected one tool-versions and one cache-toolchain step"
tool_versions_index=$(yq eval '.runs.steps | to_entries | map(select(.value.id == "tool-versions")) | .[0].key' "$ACTION_FILE")
cache_index=$(yq eval '.runs.steps | to_entries | map(select(.value.id == "cache-toolchain")) | .[0].key' "$ACTION_FILE")
(( tool_versions_index < cache_index )) || fail "tool-versions must run before cache-toolchain"
cache_key=$(yq eval '.runs.steps[] | select(.id == "cache-toolchain") | .with.key' "$ACTION_FILE")
expected_cache_key="\${{ runner.os }}-\${{ runner.environment }}-\${{ inputs.k8sVersion }}-go-\${{ steps.setup-go.outputs.go-version }}-ginkgo-\${{ steps.tool-versions.outputs.ginkgo }}-toolchain-cache-\${{ hashFiles('hack/toolchain.sh', 'hack/go-tool-path.sh') }}"
[[ "$cache_key" == "$expected_cache_key" ]] || fail "cache key differs from the exact Ginkgo-scoped key"
cache_paths=$(yq eval '.runs.steps[] | select(.id == "cache-toolchain") | .with.path' "$ACTION_FILE")
[[ "$cache_paths" == *'${{ steps.tool-versions.outputs.go_bin }}'* ]] || fail "cache does not use the effective Go bin directory"
producer_script=$(yq eval '.runs.steps[] | select(.id == "tool-versions") | .run' "$ACTION_FILE")
printf '%s\n' "$producer_script" > "$TEMP_DIR/resolve-action-version.sh"

run_action_lookup() {
    local mode="$1"
    local expected_version="$2"
    local case_dir="$TEMP_DIR/action-$mode-${expected_version//./-}"
    mkdir -p "$case_dir/bin"
    cat > "$case_dir/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$#" == 7 && "$1" == "-C" && "$2" == "$GITHUB_WORKSPACE" && "$3" == "list" && "$4" == "-m" && "$5" == "-f" && "$6" == "{{ .Version }}" && "$7" == "github.com/onsi/ginkgo/v2" ]]; then
    [[ "${GOWORK:-}" == "off" ]] || exit 89
    case "$ACTION_LOOKUP_MODE" in
        value) printf '%s\n' "$ACTION_EXPECTED_VERSION" ;;
        value-fail) printf '%s\n' "$ACTION_EXPECTED_VERSION"; exit 42 ;;
        empty) ;;
        fail) exit 42 ;;
        *) exit 90 ;;
    esac
elif [[ "$#" == 2 && "$1" == "env" && "$2" == "GOBIN" ]]; then
    printf '%s\n' "$ACTION_FAKE_GOBIN"
elif [[ "$#" == 2 && "$1" == "env" && "$2" == "GOPATH" ]]; then
    printf '%s\n' "$ACTION_FAKE_GOPATH"
else
    exit 88
fi
EOF
    chmod +x "$case_dir/bin/go"
    : > "$case_dir/output"

    if PATH="$case_dir/bin:$PATH" GITHUB_WORKSPACE="$REPO_ROOT" GITHUB_OUTPUT="$case_dir/output" ACTION_LOOKUP_MODE="$mode" \
        ACTION_EXPECTED_VERSION="$expected_version" ACTION_FAKE_GOBIN="$case_dir/gobin" ACTION_FAKE_GOPATH="$case_dir/gopath-one:$case_dir/gopath-two" \
        bash -e -o pipefail "$TEMP_DIR/resolve-action-version.sh"; then
        [[ "$mode" == value ]] || fail "action $mode lookup unexpectedly succeeded"
        [[ "$(<"$case_dir/output")" == "ginkgo=$expected_version"$'\n'"go_bin=$case_dir/gobin" ]] || fail "action emitted wrong tool outputs"
    else
        [[ "$mode" != value ]] || fail "action value lookup unexpectedly failed"
        [[ ! -s "$case_dir/output" ]] || fail "action $mode lookup emitted a cache-key value"
    fi
}

run_action_lookup value v9.8.7
run_action_lookup value v1.2.3
run_action_lookup empty v9.8.7
run_action_lookup fail v9.8.7
run_action_lookup value-fail v9.8.7
