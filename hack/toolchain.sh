#!/usr/bin/env bash
set -euo pipefail

# Keep the default version aligned with .github/actions/install-deps/action.yaml
# and jobs.ci.env.K8S_VERSION in .github/workflows/ci.yml.
K8S_VERSION="${K8S_VERSION:-1.34.x}"
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-/usr/local/kubebuilder/bin}"
SETUP_ENVTEST_BIN="${SETUP_ENVTEST_BIN:-${KUBEBUILDER_ASSETS}/setup-envtest}"
SETUP_ENVTEST_VERSION="v0.22.3"
SETUP_ENVTEST_URL="${SETUP_ENVTEST_URL:-}"
SETUP_ENVTEST_SHA256="${SETUP_ENVTEST_SHA256:-}"
SKIP_INSTALLED="${SKIP_INSTALLED:-false}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "${SCRIPT_DIR}")"
TOOL_DEST="${TOOL_DEST:-$(go env GOPATH)/bin}"

if [ "$SKIP_INSTALLED" == true ]; then
    echo "[INF] Skipping tools already installed."
fi

main() {
    crosscompilers
    tools
    kubebuilder
    gettrivy
}

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
        [[ -f "${command_name}" && -x "${command_name}" ]] || return 1
        printf '%s\n' "${command_name}"
        return
    fi
    local command_path
    command_path="$(command -v "${command_name}" 2>/dev/null)" || return 1
    [[ -f "${command_path}" && -x "${command_path}" ]] || return 1
    printf '%s\n' "${command_path}"
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
    # -F matches fixed text, -q suppresses output, and -w requires word boundaries.
    grep -Fqw -- "$3" <<<"${output}"
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

# go-install installs a Go tool unless should-skip confirms it is already suitable.
#
# Presence-only form:
#   go-install <app> <reference>
#
# Version-aware form:
#   go-install <app> <version|--version> <expected-text> <reference>
#
# <app> is the binary name under TOOL_DEST, <expected-text> must appear in the
# version command output, and <reference> is the package passed to go install.
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

crosscompilers() {
    # Install CGO cross-compilation toolchains for multi-arch builds
    if ! command -v aarch64-linux-gnu-gcc &> /dev/null || ! command -v x86_64-linux-gnu-gcc &> /dev/null; then
        sudo apt-get update
        sudo apt-get install -y gcc-aarch64-linux-gnu gcc-x86-64-linux-gnu
    fi
}

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

tools() {
    local ginkgo_version
    ginkgo_version="$(_ginkgo_version)"

    go-install go-licenses github.com/google/go-licenses/v2@3e084b0caf710f7bfead967567539214f598c0a2 # v2.0.1
    go-install ko version v0.17.1 github.com/google/ko@v0.17.1
    go-install yq --version v4.45.1 github.com/mikefarah/yq/v4@v4.45.1
    go-install helm-docs github.com/norwoodj/helm-docs/cmd/helm-docs@v1.14.2
    go-install controller-gen --version v0.19.0 sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
    go-install cosign version v2.4.1 github.com/sigstore/cosign/v2/cmd/cosign@v2.4.1
#   go install -tags extended github.com/gohugoio/hugo@v0.110.0
    go-install govulncheck --version v1.8.0 golang.org/x/vuln/cmd/govulncheck@v1.8.0
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

    go-install golangci-lint --version 2.14.0 github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

    # Install our custom modules in golangci-lint
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
    arch=$(go env GOARCH)
    os=$(go env GOOS)
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

kubebuilder() {
    echo "[INF] Setting up kubebuilder binaries for Kubernetes ${K8S_VERSION}"
    _ensure_writable_directory "${KUBEBUILDER_ASSETS}"
    _install_setup_envtest
    _ensure_envtest_assets
}

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

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
