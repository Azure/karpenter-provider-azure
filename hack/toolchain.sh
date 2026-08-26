#!/usr/bin/env bash
set -euo pipefail

K8S_VERSION="${K8S_VERSION:="1.29.x"}"
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:=/usr/local/kubebuilder/bin}"
SETUP_ENVTEST_BIN="${SETUP_ENVTEST_BIN:=/usr/local/bin/setup-envtest}"

# Default SKIP_INSTALLED to false if not set
SKIP_INSTALLED="${SKIP_INSTALLED:=false}"

# Find the repository and the effective directory used by go install.
SCRIPT_DIR=$(dirname "$(realpath "$0")")
REPO_ROOT=$(dirname "$SCRIPT_DIR")
TOOL_DEST=$("$SCRIPT_DIR/go-tool-path.sh")

if [ "$SKIP_INSTALLED" == true ]; then
    echo "[INF] Skipping tools already installed."
fi

main() {
    crosscompilers
    tools
    kubebuilder
    gettrivy
}

# should-skip is a helper function to determine if installation of a tool should be skipped
# $1 is the expected command
should-skip() {
    if [ "$SKIP_INSTALLED" == true ] && [ -f "$TOOL_DEST/$1" ]; then
        # We can skip installation
        return 0
    fi

    # Installation is needed
    return 1
}

# go-install is a helper function to install go tools
# $1 is the expected command
# $2 is the go install path
go-install() {
    # Check to see if we need to install
    if should-skip "$1"; then
        # Silently skip, to avoid console debris
        # echo "[INF] $1 is already installed, skipping."
        return
    fi

    echo "[INF] Installing $1"
    go install "$2"
}

# Ginkgo's CLI must match the library imported by this repository. Unlike other
# tools, an existing binary is skipped only when its version matches go.mod.
install-ginkgo() {
    local module="github.com/onsi/ginkgo/v2"
    local expected_version
    if ! expected_version=$(GOWORK=off go -C "$REPO_ROOT" list -m -f '{{ .Version }}' "$module"); then
        echo "failed to resolve Ginkgo version from $REPO_ROOT/go.mod" >&2
        return 1
    fi
    if [[ -z "$expected_version" ]]; then
        echo "resolved an empty Ginkgo version from $REPO_ROOT/go.mod" >&2
        return 1
    fi

    if should-skip ginkgo; then
        local installed_version
        if ! installed_version=$("$TOOL_DEST/ginkgo" version 2>/dev/null | awk '$1 == "Ginkgo" && $2 == "Version" { print "v" $3; exit }'); then
            installed_version=""
        fi
        if [[ "$installed_version" == "$expected_version" ]]; then
            return
        fi
    fi

    echo "[INF] Installing ginkgo $expected_version"
    go install "$module/ginkgo@$expected_version"
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
    install-ginkgo
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

link-kubebuilder-assets() {
    local arch="$1"
    local asset_dir
    if ! asset_dir=$("$SETUP_ENVTEST_BIN" use -p path "$K8S_VERSION" --arch="$arch" --bin-dir="$KUBEBUILDER_ASSETS"); then
        echo "setup-envtest failed to resolve Kubernetes $K8S_VERSION assets" >&2
        return 1
    fi
    if [[ -z "$asset_dir" || "$asset_dir" != /* || "$asset_dir" == "/" || ! -d "$asset_dir" ]]; then
        echo "setup-envtest returned an invalid asset directory: ${asset_dir:-<empty>}" >&2
        return 1
    fi
    if [[ ! -d "$KUBEBUILDER_ASSETS" ]]; then
        echo "kubebuilder asset destination does not exist: $KUBEBUILDER_ASSETS" >&2
        return 1
    fi

    local -a required_binaries=(kube-apiserver kubectl etcd)
    local -a destinations=()
    local -a old_targets=()
    local -a old_exists=()
    local binary source destination
    for binary in "${required_binaries[@]}"; do
        source="$asset_dir/$binary"
        destination="$KUBEBUILDER_ASSETS/$binary"
        if [[ ! -f "$source" || ! -x "$source" ]]; then
            echo "setup-envtest asset is missing regular executable $binary: $source" >&2
            return 1
        fi
        if [[ -e "$destination" && ! -L "$destination" ]]; then
            echo "refusing to replace non-symlink kubebuilder asset: $destination" >&2
            return 1
        fi
        if [[ -L "$destination" && -d "$destination" ]]; then
            echo "refusing to replace symlink to directory: $destination" >&2
            return 1
        fi
        destinations+=("$destination")
        if [[ -L "$destination" ]]; then
            old_targets+=("$(readlink "$destination")")
            old_exists+=(1)
        else
            old_targets+=("")
            old_exists+=(0)
        fi
    done

    local i j
    for i in "${!required_binaries[@]}"; do
        source="$asset_dir/${required_binaries[$i]}"
        destination="${destinations[$i]}"
        if ! ln -sfnT -- "$source" "$destination"; then
            echo "failed to link kubebuilder asset: $destination" >&2
            for ((j = 0; j <= i; j++)); do
                if [[ "${old_exists[$j]}" == 1 ]]; then
                    ln -sfnT -- "${old_targets[$j]}" "${destinations[$j]}" || \
                        echo "failed to roll back kubebuilder asset: ${destinations[$j]}" >&2
                else
                    rm -f -- "${destinations[$j]}"
                fi
            done
            return 1
        fi
    done
}

install-setup-envtest() {
    local os="$1"
    local arch="$2"
    local download
    download=$(mktemp)
    if ! curl --fail --silent --show-error --location --retry 3 --retry-delay 2 --retry-all-errors \
        --retry-max-time 180 --connect-timeout 20 --max-time 120 \
        "https://github.com/kubernetes-sigs/controller-runtime/releases/download/v0.22.3/setup-envtest-${os}-${arch}" \
        --output "$download"; then
        rm -f -- "$download"
        return 1
    fi
    local install_dir staged
    install_dir=$(dirname "$SETUP_ENVTEST_BIN")
    if ! staged=$(sudo mktemp "$install_dir/.setup-envtest.XXXXXX"); then
        rm -f -- "$download"
        return 1
    fi
    if ! sudo install -m 0755 "$download" "$staged"; then
        rm -f -- "$download"
        sudo rm -f -- "$staged"
        return 1
    fi
    if ! sudo mv -fT -- "$staged" "$SETUP_ENVTEST_BIN"; then
        rm -f -- "$download"
        sudo rm -f -- "$staged"
        return 1
    fi
    rm -f -- "$download"
}

kubebuilder() {
    echo "[INF] Setting up kubebuilder binaries for Kubernetes ${K8S_VERSION}"
    sudo mkdir -p "${KUBEBUILDER_ASSETS}"
    sudo chown "${USER}" "${KUBEBUILDER_ASSETS}"
    arch=$(go env GOARCH)
    os=$(go env GOOS)
    install-setup-envtest "$os" "$arch"
    link-kubebuilder-assets "$arch"
    find "$KUBEBUILDER_ASSETS" -maxdepth 2

    # Install latest binaries for 1.25.x (contains CEL fix)
    if [[ "${K8S_VERSION}" = "1.25.x" ]] && [[ "$OSTYPE" == "linux"* ]]; then
        for binary in 'kube-apiserver' 'kubectl'; do
            rm -- "$KUBEBUILDER_ASSETS/$binary"
            wget -P "$KUBEBUILDER_ASSETS" "https://dl.k8s.io/v1.25.16/bin/linux/${arch}/${binary}"
            chmod +x "$KUBEBUILDER_ASSETS/$binary"
        done
    fi
}

gettrivy() {
    if ! command -v trivy &> /dev/null; then
        TRIVY_VERSION="0.69.3"
        TRIVY_SHA256="a484057aafde31089cf2558ca0f79a4bc835125a5ee6834183a5bcf0735af358"
        wget -qO /tmp/trivy.deb "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-64bit.deb"
        echo "${TRIVY_SHA256}  /tmp/trivy.deb" | sha256sum --check --strict
        sudo dpkg -i /tmp/trivy.deb
        rm /tmp/trivy.deb
    fi
}

main "$@"
