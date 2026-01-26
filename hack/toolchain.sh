#!/usr/bin/env bash
set -euo pipefail

K8S_VERSION="${K8S_VERSION:="1.29.x"}"
KUBEBUILDER_ASSETS="/usr/local/kubebuilder/bin"

# Default SKIP_INSTALLED to false if not set
SKIP_INSTALLED="${SKIP_INSTALLED:=false}"

# Find the path where this script is found
SCRIPT_DIR=$(dirname "$(realpath "$0")")

# Define go install will put things
TOOL_DEST="$(go env GOPATH)/bin"

if [ "$SKIP_INSTALLED" == true ]; then
    echo "[INF] Skipping tools already installed."
fi

# This is where go install will put things
TOOL_DEST="$(go env GOPATH)/bin"

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

crosscompilers() {
    # Install CGO cross-compilation toolchains for multi-arch builds
    if ! command -v aarch64-linux-gnu-gcc &> /dev/null || ! command -v x86_64-linux-gnu-gcc &> /dev/null; then
        sudo apt-get update
        sudo apt-get install -y gcc-aarch64-linux-gnu gcc-x86-64-linux-gnu
    fi
}

tools() {
    go-install go-licenses github.com/google/go-licenses@v1.6.0
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

    go-install golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0

    # Install our custom modules in golangci-lint
    if ! should-skip "golangci-lint-custom"; then
        echo "[INF] Installing golangci-lint custom modules"
        TOOL_DEST=$TOOL_DEST envsubst < "$SCRIPT_DIR/custom-gcl.template.yml" > .custom-gcl.yml
        "$TOOL_DEST/golangci-lint" custom -v
        rm .custom-gcl.yml
        # mv "$TOOL_DEST/golangci-lint-custom" "$TOOL_DEST/golangci-lint"
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
            rm $KUBEBUILDER_ASSETS/$binary
            wget -P $KUBEBUILDER_ASSETS https://dl.k8s.io/v1.25.16/bin/linux/"${arch}"/${binary}
            chmod +x $KUBEBUILDER_ASSETS/$binary
        done
    fi
}

gettrivy() {
    if ! command -v trivy &> /dev/null; then
        wget -qO - https://aquasecurity.github.io/trivy-repo/deb/public.key | gpg --dearmor | sudo tee /usr/share/keyrings/trivy.gpg > /dev/null
        echo "deb [signed-by=/usr/share/keyrings/trivy.gpg] https://aquasecurity.github.io/trivy-repo/deb generic main" | sudo tee -a /etc/apt/sources.list.d/trivy.list
        sudo apt-get update
        sudo apt-get install -y trivy
    fi
}

main "$@"
