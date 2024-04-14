#!/usr/bin/env bash
set -euo pipefail

main() {
    tools
}

tools() {
    sudo apt update && sudo apt install curl jq -y
    curl -Lo yq https://github.com/mikefarah/yq/releases/download/v4.43.1/yq_linux_amd64 && chmod +x yq && sudo mv yq /usr/local/bin
    if test -z "$(which helm)"; then \
        curl -sL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash -
    fi
    if test -z "$(which docker)"; then \
        curl -fsSL https://get.docker.com | bash -; \
    fi
    if test -z "$(which kubectl)"; then \
        curl -sLO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && chmod +x kubectl && sudo mv kubectl /usr/local/bin; \
    fi
    if test -z "$(which go)" || [ $(go version | awk -F'[ .]' '{print$3"."$4}') != $(grep -E '^go' go.mod | sed 's/ //g') ]; then \
        curl -sL https://go.dev/dl/go1.22.1.linux-amd64.tar.gz | sudo tar xvzf - -C /usr/local/; \
    fi
    if test -z "$(which skaffold)"; then \
        curl -sLo skaffold https://storage.googleapis.com/skaffold/releases/v2.10.1/skaffold-linux-amd64 && chmod +x skaffold && sudo mv skaffold /usr/local/bin; \
        skaffold config set --global collect-metrics false; \
    fi
}

main "$@"
