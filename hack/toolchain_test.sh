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

run_kubebuilder_asset_case() {
    local mode="$1"
    local expected_result="$2"
    local case_dir="$TEMP_DIR/kubebuilder-$mode"
    local source_dir="$case_dir/source dir"
    local target_dir="$case_dir/target"
    local call_record="$case_dir/setup-envtest-call"
    local error_record="$case_dir/error"
    mkdir -p "$case_dir/bin" "$source_dir" "$target_dir"

    case "$mode" in
        valid|missing-binary|directory-binary|destination-directory|destination-symlink-directory|link-failure|output-fail)
            for binary in kube-apiserver kubectl etcd; do
                if [[ "$mode" == "missing-binary" && "$binary" == "etcd" ]]; then
                    continue
                fi
                printf '#!/usr/bin/env bash\nexit 0\n' > "$source_dir/$binary"
                chmod +x "$source_dir/$binary"
            done
            ;;
    esac
    if [[ "$mode" == "directory-binary" ]]; then
        rm "$source_dir/etcd"
        mkdir "$source_dir/etcd"
    elif [[ "$mode" == "destination-directory" ]]; then
        mkdir "$target_dir/kubectl"
    elif [[ "$mode" == "destination-symlink-directory" ]]; then
        mkdir "$case_dir/collision-dir"
        ln -s "$case_dir/collision-dir" "$target_dir/kubectl"
    elif [[ "$mode" == "link-failure" ]]; then
        mkdir "$case_dir/old-assets"
        for binary in kube-apiserver kubectl etcd; do
            printf 'old-%s\n' "$binary" > "$case_dir/old-assets/$binary"
            ln -s "$case_dir/old-assets/$binary" "$target_dir/$binary"
        done
        cat > "$case_dir/bin/ln" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
count=0
[[ -f "$LN_CALL_COUNT" ]] && count=$(<"$LN_CALL_COUNT")
count=$((count + 1))
printf '%s\n' "$count" > "$LN_CALL_COUNT"
if [[ "$count" == 2 ]]; then
    exit 73
fi
exec /usr/bin/ln "$@"
EOF
        chmod +x "$case_dir/bin/ln"
    fi

    cat > "$case_dir/bin/setup-envtest" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "$SETUP_ENVTEST_CALL"
case "$SETUP_ENVTEST_MODE" in
    valid|missing-binary|directory-binary|destination-directory|destination-symlink-directory|link-failure) printf '%s\n' "$SETUP_ENVTEST_SOURCE" ;;
    output-fail) printf '%s\n' "$SETUP_ENVTEST_SOURCE"; exit 42 ;;
    empty) ;;
    fail) exit 42 ;;
    non-directory) printf '%s\n' "$SETUP_ENVTEST_SOURCE/not-a-directory" ;;
    root) printf '/\n' ;;
    *) exit 90 ;;
esac
EOF
    chmod +x "$case_dir/bin/setup-envtest"
    grep -v '^main "\$@"$' "$TOOLCHAIN_SCRIPT" > "$case_dir/toolchain-library.sh"
    before_links=$(find "$target_dir" -maxdepth 1 -type l -printf '%f -> %l\n' | sort)

    if (
        export KUBEBUILDER_ASSETS="$target_dir" K8S_VERSION="1.29.x"
        export SETUP_ENVTEST_BIN="$case_dir/bin/setup-envtest"
        export SETUP_ENVTEST_CALL="$call_record" SETUP_ENVTEST_MODE="$mode" SETUP_ENVTEST_SOURCE="$source_dir"
        export LN_CALL_COUNT="$case_dir/ln-count" PATH="$case_dir/bin:$PATH"
        # shellcheck source=/dev/null
        source "$case_dir/toolchain-library.sh"
        link-kubebuilder-assets amd64
    ) 2>"$error_record"; then
        [[ "$expected_result" == "success" ]] || fail "$mode setup-envtest unexpectedly succeeded"
    else
        [[ "$expected_result" == "failure" ]] || fail "$mode setup-envtest unexpectedly failed"
    fi

    expected_call=$'use\n-p\npath\n1.29.x\n--arch=amd64\n'"--bin-dir=$target_dir"
    [[ "$(<"$call_record")" == "$expected_call" ]] || fail "$mode used unexpected setup-envtest argument boundaries"
    after_links=$(find "$target_dir" -maxdepth 1 -type l -printf '%f -> %l\n' | sort)
    if [[ "$expected_result" == "success" ]]; then
        [[ "$(printf '%s\n' "$after_links" | grep -c -- ' -> ')" == 3 ]] || fail "$mode did not create exactly 3 symlinks"
        for binary in kube-apiserver kubectl etcd; do
            [[ "$(readlink "$target_dir/$binary")" == "$source_dir/$binary" ]] || fail "$mode linked $binary to the wrong target"
        done
    else
        [[ "$after_links" == "$before_links" ]] || fail "$mode changed destination links after failure"
    fi
    if [[ "$mode" == "root" ]]; then
        grep -Fq 'invalid asset directory: /' "$error_record" || fail "root path was rejected only incidentally"
    fi
}

run_kubebuilder_asset_case valid success
run_kubebuilder_asset_case empty failure
run_kubebuilder_asset_case fail failure
run_kubebuilder_asset_case output-fail failure
run_kubebuilder_asset_case non-directory failure
run_kubebuilder_asset_case root failure
run_kubebuilder_asset_case missing-binary failure
run_kubebuilder_asset_case directory-binary failure
run_kubebuilder_asset_case destination-directory failure
run_kubebuilder_asset_case destination-symlink-directory failure
run_kubebuilder_asset_case link-failure failure

write_fake_curl() {
    local path="$1"
    cat > "$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "$CURL_CALLS"
output=""
while (( $# )); do
    if [[ "$1" == "--output" ]]; then
        output="$2"
        break
    fi
    shift
done
[[ -n "$output" ]] || exit 88
if [[ "$CURL_MODE" == "success" ]]; then
    cat > "$output" <<'SCRIPT'
#!/usr/bin/env bash
if [[ -n "${SETUP_ENVTEST_CALL:-}" ]]; then
    printf '%s\n' "$@" > "$SETUP_ENVTEST_CALL"
fi
printf '%s\n' "$SETUP_ENVTEST_SOURCE"
SCRIPT
else
    printf 'partial' > "$output"
    exit 18
fi
EOF
    chmod +x "$path"
}

run_setup_envtest_download_case() {
    local mode="$1"
    local case_dir="$TEMP_DIR/setup-download-$mode"
    local destination="$case_dir/existing-setup-envtest"
    local calls="$case_dir/curl-calls"
    mkdir -p "$case_dir/bin"
    printf 'sentinel' > "$destination"
    cat > "$case_dir/bin/sudo" <<'EOF'
#!/usr/bin/env bash
exec "$@"
EOF
    chmod +x "$case_dir/bin/sudo"
    if [[ "$mode" == "install-failure" ]]; then
        cat > "$case_dir/bin/install" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
destination=${!#}
printf 'partial-install' > "$destination"
exit 153
EOF
        chmod +x "$case_dir/bin/install"
    fi
    write_fake_curl "$case_dir/bin/curl"
    grep -v '^main "\$@"$' "$TOOLCHAIN_SCRIPT" > "$case_dir/toolchain-library.sh"

    if (
        export SETUP_ENVTEST_BIN="$destination" CURL_CALLS="$calls"
        [[ "$mode" == "failure" ]] && export CURL_MODE=failure || export CURL_MODE=success
        export PATH="$case_dir/bin:$PATH"
        # shellcheck source=/dev/null
        source "$case_dir/toolchain-library.sh"
        install-setup-envtest linux amd64
    ); then
        [[ "$mode" == "success" ]] || fail "$mode setup-envtest installation unexpectedly succeeded"
    else
        [[ "$mode" != "success" ]] || fail "setup-envtest download unexpectedly failed"
    fi

    mapfile -t curl_args < "$calls"
    expected_prefix=(
        --fail --silent --show-error --location --retry 3 --retry-delay 2 --retry-all-errors
        --retry-max-time 180 --connect-timeout 20 --max-time 120
        https://github.com/kubernetes-sigs/controller-runtime/releases/download/v0.22.3/setup-envtest-linux-amd64
        --output
    )
    [[ "${#curl_args[@]}" == $((${#expected_prefix[@]} + 1)) ]] || fail "curl received an unexpected argument count"
    for i in "${!expected_prefix[@]}"; do
        [[ "${curl_args[$i]}" == "${expected_prefix[$i]}" ]] || fail "curl argument $i differs: ${curl_args[$i]}"
    done
    temp_download=${curl_args[$((${#curl_args[@]} - 1))]}
    [[ "$temp_download" != "$destination" && ! -e "$temp_download" ]] || fail "temporary setup-envtest download was not cleaned"
    if [[ "$mode" == "success" ]]; then
        [[ -x "$destination" && "$(<"$destination")" != "sentinel" ]] || fail "successful setup-envtest download was not installed"
    else
        [[ "$(<"$destination")" == "sentinel" ]] || fail "$mode replaced the existing setup-envtest"
    fi
    [[ -z "$(find "$case_dir" -maxdepth 1 -name '.setup-envtest.*' -print -quit)" ]] || fail "$mode left a staged setup-envtest file"
}

run_setup_envtest_download_case success
run_setup_envtest_download_case failure
run_setup_envtest_download_case install-failure

run_kubebuilder_125_path_case() {
    local case_dir="$TEMP_DIR/kubebuilder-125"
    local source_dir="$case_dir/source"
    local target_dir="$case_dir/target dir"
    mkdir -p "$case_dir/bin" "$source_dir" "$target_dir"
    for binary in kube-apiserver kubectl etcd; do
        printf '#!/usr/bin/env bash\nexit 0\n' > "$source_dir/$binary"
        chmod +x "$source_dir/$binary"
    done
    cat > "$case_dir/bin/sudo" <<'EOF'
#!/usr/bin/env bash
exec "$@"
EOF
    cat > "$case_dir/bin/wget" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$#" == 3 && "$1" == "-P" ]] || exit 88
name=${3##*/}
printf '#!/usr/bin/env bash\nexit 0\n' > "$2/$name"
EOF
    chmod +x "$case_dir/bin/sudo" "$case_dir/bin/wget"
    export SETUP_ENVTEST_SOURCE="$source_dir" SETUP_ENVTEST_CALL="$case_dir/setup-envtest-call"
    export CURL_CALLS="$case_dir/curl-calls" CURL_MODE=success
    write_fake_curl "$case_dir/bin/curl"
    grep -v '^main "\$@"$' "$TOOLCHAIN_SCRIPT" > "$case_dir/toolchain-library.sh"
    (
        export KUBEBUILDER_ASSETS="$target_dir" K8S_VERSION="1.25.x"
        export SETUP_ENVTEST_BIN="$case_dir/installed-setup-envtest"
        export PATH="$case_dir/bin:$PATH"
        # shellcheck source=/dev/null
        source "$case_dir/toolchain-library.sh"
        kubebuilder
    ) >/dev/null
    expected_arch=$(go env GOARCH)
    expected_call=$'use\n-p\npath\n1.25.x\n'"--arch=$expected_arch"$'\n'"--bin-dir=$target_dir"
    [[ "$(<"$SETUP_ENVTEST_CALL")" == "$expected_call" ]] || fail "1.25 setup-envtest arguments split the spaced path"
    [[ -f "$target_dir/kube-apiserver" && -x "$target_dir/kube-apiserver" ]] || fail "1.25 kube-apiserver replacement failed with spaced path"
    [[ -f "$target_dir/kubectl" && -x "$target_dir/kubectl" ]] || fail "1.25 kubectl replacement failed with spaced path"
    [[ -L "$target_dir/etcd" ]] || fail "1.25 etcd setup was lost"
}

run_kubebuilder_125_path_case
