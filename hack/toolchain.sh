#!/usr/bin/env bash
set -euo pipefail

K8S_VERSION="${K8S_VERSION:="1.29.x"}"
KUBEBUILDER_ASSETS="/usr/local/kubebuilder/bin"

main() {
    tools
    kubebuilder
    gettrivy
}

tools() {
    go install github.com/google/go-licenses@v1.6.0
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0
    go install github.com/google/ko@v0.17.1
    go install github.com/mikefarah/yq/v4@v4.45.1
    go install github.com/norwoodj/helm-docs/cmd/helm-docs@v1.14.2
    go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0
    go install github.com/sigstore/cosign/v2/cmd/cosign@v2.4.1
#   go install -tags extended github.com/gohugoio/hugo@v0.110.0
    go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
    go install github.com/onsi/ginkgo/v2/ginkgo@latest
    go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.7
    go install github.com/mattn/goveralls@v0.0.12
    go install github.com/google/go-containerregistry/cmd/crane@v0.20.2
    go install github.com/go-swagger/go-swagger/cmd/swagger@v0.33.1
    go install github.com/Azure/aks-node-viewer/cmd/aks-node-viewer@latest
    go install github.com/google/pprof@latest

    if ! echo "$PATH" | grep -q "${GOPATH:-undefined}/bin\|$HOME/go/bin"; then
        echo "Go workspace's \"bin\" directory is not in PATH. Run 'export PATH=\"\$PATH:\${GOPATH:-\$HOME/go}/bin\"'."
    fi
}

kubebuilder() {
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
