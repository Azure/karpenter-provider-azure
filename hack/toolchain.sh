#!/usr/bin/env bash
set -euo pipefail

K8S_VERSION="${K8S_VERSION:="1.29.x"}"
KUBEBUILDER_ASSETS="/usr/local/kubebuilder/bin"

main() {
    tools
    kubebuilder
}

tools() {
    go install github.com/google/go-licenses@v1.6.0
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.57.2
    go install github.com/google/ko@v0.15.2
    go install github.com/mikefarah/yq/v4@v4.43.1
    go install github.com/norwoodj/helm-docs/cmd/helm-docs@v1.13.1
    go install sigs.k8s.io/controller-runtime/tools/setup-envtest@0c7827e417acc15f29e7c4bfccede809d372676a 
    go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.14.0
    go install github.com/sigstore/cosign/v2/cmd/cosign@v2.2.4
#   go install -tags extended github.com/gohugoio/hugo@v0.110.0
    go install golang.org/x/vuln/cmd/govulncheck@v1.0.4
    go install github.com/onsi/ginkgo/v2/ginkgo@latest
    go install github.com/rhysd/actionlint/cmd/actionlint@v1.6.27
    go install github.com/mattn/goveralls@v0.0.12
    go install github.com/google/go-containerregistry/cmd/crane@v0.19.1

    if ! echo "$PATH" | grep -q "${GOPATH:-undefined}/bin\|$HOME/go/bin"; then
        echo "Go workspace's \"bin\" directory is not in PATH. Run 'export PATH=\"\$PATH:\${GOPATH:-\$HOME/go}/bin\"'."
    fi
}

kubebuilder() {
    sudo mkdir -p "${KUBEBUILDER_ASSETS}"
    sudo chown "${USER}" "${KUBEBUILDER_ASSETS}"
    arch=$(go env GOARCH)
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

main "$@"
