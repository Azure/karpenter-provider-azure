#!/usr/bin/env bash
set -euo pipefail

K8S_VERSION="${K8S_VERSION:-1.34.x}"
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-/usr/local/kubebuilder/bin}"
SKIP_INSTALLED="${SKIP_INSTALLED:-false}"
FORCE_INSTALL="${FORCE_INSTALL:-false}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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

    if [[ "${FORCE_INSTALL}" == true ]]; then
        echo "[INF] Installing $1 because FORCE_INSTALL=true"
        return 1
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

crosscompilers() {
    # Install CGO cross-compilation toolchains for multi-arch builds
    if ! command -v aarch64-linux-gnu-gcc &> /dev/null || ! command -v x86_64-linux-gnu-gcc &> /dev/null; then
        sudo apt-get update
        sudo apt-get install -y gcc-aarch64-linux-gnu gcc-x86-64-linux-gnu
    fi
}

tools() {
    go-install go-licenses github.com/google/go-licenses/v2@3e084b0caf710f7bfead967567539214f598c0a2 // v2.0.1
    go-install ko github.com/google/ko@v0.17.1
    go-install yq github.com/mikefarah/yq/v4@v4.45.1
    go-install helm-docs github.com/norwoodj/helm-docs/cmd/helm-docs@v1.14.2
    go-install controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
    go-install cosign github.com/sigstore/cosign/v2/cmd/cosign@v2.4.1
#   go install -tags extended github.com/gohugoio/hugo@v0.110.0
    go-install govulncheck golang.org/x/vuln/cmd/govulncheck@v1.1.4
    go-install ginkgo github.com/onsi/ginkgo/v2/ginkgo@latest
    go-install actionlint github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
    go-install goveralls github.com/mattn/goveralls@v0.0.12
    go-install crane github.com/google/go-containerregistry/cmd/crane@v0.20.2
    go-install swagger github.com/go-swagger/go-swagger/cmd/swagger@v0.33.1
    go-install aks-node-viewer github.com/Azure/aks-node-viewer/cmd/aks-node-viewer@latest
    go-install pprof github.com/google/pprof@latest

    if ! echo "$PATH" | grep -q "${GOPATH:-undefined}/bin\|$HOME/go/bin"; then
        echo "Go workspace's \"bin\" directory is not in PATH. Run 'export PATH=\"\$PATH:\${GOPATH:-\$HOME/go}/bin\"'."
    fi

    go-install golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

    # Install our custom modules in golangci-lint
    if ! should-skip "golangci-lint-custom"; then
        echo "[INF] Installing golangci-lint custom modules"
        TOOL_DEST=$TOOL_DEST envsubst < "$SCRIPT_DIR/custom-gcl.template.yml" > .custom-gcl.yml
        "$TOOL_DEST/golangci-lint" custom -v
        rm .custom-gcl.yml
    fi
}

kubebuilder() {
    echo "[INF] Setting up kubebuilder binaries for Kubernetes ${K8S_VERSION}"
    sudo mkdir -p "${KUBEBUILDER_ASSETS}"
    sudo chown "${USER}" "${KUBEBUILDER_ASSETS}"
    arch=$(go env GOARCH)
    os=$(go env GOOS)
    sudo curl -sL "https://github.com/kubernetes-sigs/controller-runtime/releases/download/v0.22.3/setup-envtest-${os}-${arch}" --output /usr/local/bin/setup-envtest
    sudo chmod +x /usr/local/bin/setup-envtest

    ln -sf "$(setup-envtest use -p path "${K8S_VERSION}" --arch="${arch}" --bin-dir="${KUBEBUILDER_ASSETS}")"/* "${KUBEBUILDER_ASSETS}"
    find "$KUBEBUILDER_ASSETS"

    # Install latest binaries for 1.25.x (contains CEL fix)
    if [[ "${K8S_VERSION}" = "1.25.x" ]] && [[ "$OSTYPE" == "linux"* ]]; then
        for binary in 'kube-apiserver' 'kubectl'; do
            rm "${KUBEBUILDER_ASSETS}/${binary}"
            wget -P "${KUBEBUILDER_ASSETS}" https://dl.k8s.io/v1.25.16/bin/linux/"${arch}"/"${binary}"
            chmod +x "${KUBEBUILDER_ASSETS}/${binary}"
        done
    fi
}

gettrivy() {
    TRIVY_VERSION="0.74.0"
    TRIVY_SHA256="cf1e32ec8d4d8823e023096a28cadb14f5b5123ce03f201fb633c5b76aa712dd"
    if ! command -v trivy &> /dev/null || [[ "$(trivy --version | head -n 1)" != "Version: ${TRIVY_VERSION}" ]]; then
        wget -qO /tmp/trivy.deb "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-64bit.deb"
        echo "${TRIVY_SHA256}  /tmp/trivy.deb" | sha256sum --check --strict
        sudo dpkg -i /tmp/trivy.deb
        rm /tmp/trivy.deb
    fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
