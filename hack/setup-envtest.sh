#!/usr/bin/env bash
set -euo pipefail

K8S_VERSION="${K8S_VERSION:-1.34.x}"
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-/usr/local/kubebuilder/bin}"
SETUP_ENVTEST_BIN="${SETUP_ENVTEST_BIN:-/usr/local/bin/setup-envtest}"
SETUP_ENVTEST_VERSION="v0.22.3"
SETUP_ENVTEST_URL="${SETUP_ENVTEST_URL:-}"
KUBERNETES_DOWNLOAD_BASE_URL="${KUBERNETES_DOWNLOAD_BASE_URL:-https://dl.k8s.io}"

error() {
    echo "[ERR] $*" >&2
}

ensure_writable_directory() {
    local path="$1"
    if ! mkdir -p "${path}" 2>/dev/null; then
        sudo mkdir -p "${path}"
    fi
    if [[ ! -w "${path}" ]]; then
        sudo chown "${USER}" "${path}"
    fi
}

install_setup_envtest() {
    if [[ -x "${SETUP_ENVTEST_BIN}" ]] &&
        [[ "$("${SETUP_ENVTEST_BIN}" version 2>/dev/null)" == "setup-envtest version: ${SETUP_ENVTEST_VERSION}" ]]; then
        return
    fi

    local arch
    local os
    local download
    arch="$(go env GOARCH)"
    os="$(go env GOOS)"
    download="$(mktemp)"
    if [[ -z "${SETUP_ENVTEST_URL}" ]]; then
        SETUP_ENVTEST_URL="https://github.com/kubernetes-sigs/controller-runtime/releases/download/${SETUP_ENVTEST_VERSION}/setup-envtest-${os}-${arch}"
    fi

    if ! curl -fsSL "${SETUP_ENVTEST_URL}" --output "${download}"; then
        rm -f "${download}"
        return 1
    fi

    ensure_writable_directory "$(dirname "${SETUP_ENVTEST_BIN}")"
    install -m 0755 "${download}" "${SETUP_ENVTEST_BIN}"
    rm -f "${download}"
}

assets_are_complete() {
    local asset_dir="$1"
    local binary

    for binary in kube-apiserver kubectl etcd; do
        [[ -x "${asset_dir}/${binary}" ]] || return 1
    done
}

version_matches_selector() {
    local actual="$1"
    local selector="$2"
    local actual_version

    if [[ ! "${actual}" =~ Kubernetes[[:space:]]v([0-9]+\.[0-9]+\.[0-9]+) ]]; then
        return 1
    fi
    actual_version="${BASH_REMATCH[1]}"

    case "${selector}" in
        *.x | *.\*)
            [[ "${actual_version%.*}" == "${selector%.*}" ]]
            ;;
        *)
            [[ "${actual_version}" == "${selector#v}" ]]
            ;;
    esac
}

kube_apiserver_version() {
    local asset_dir="$1"
    "${asset_dir}/kube-apiserver" --version 2>&1
}

resolve_installed_assets() {
    local arch="$1"
    "${SETUP_ENVTEST_BIN}" use -i -p path "${K8S_VERSION}" \
        --arch="${arch}" \
        --bin-dir="${KUBEBUILDER_ASSETS}"
}

download_assets() {
    local arch="$1"
    "${SETUP_ENVTEST_BIN}" use --force -p path "${K8S_VERSION}" \
        --arch="${arch}" \
        --bin-dir="${KUBEBUILDER_ASSETS}"
}

verify_assets() {
    local asset_dir="$1"
    local actual_version

    actual_version="$(kube_apiserver_version "${asset_dir}")"
    if ! version_matches_selector "${actual_version}" "${K8S_VERSION}"; then
        error "requested Kubernetes version: ${K8S_VERSION}"
        error "actual kube-apiserver version: ${actual_version}"
        return 1
    fi
    echo "${actual_version}"
}

link_assets() {
    local asset_dir="$1"
    local binary

    for binary in kube-apiserver kubectl etcd; do
        if [[ ! -x "${asset_dir}/${binary}" ]]; then
            error "envtest asset is missing or not executable: ${asset_dir}/${binary}"
            return 1
        fi
        ln -sfn "${asset_dir}/${binary}" "${KUBEBUILDER_ASSETS}/${binary}"
    done
}

install_kubernetes_125_cel_fix() {
    local arch="$1"
    local binary

    if [[ "${K8S_VERSION}" != "1.25.x" ]] || [[ "${OSTYPE}" != "linux"* ]]; then
        return
    fi

    for binary in kube-apiserver kubectl; do
        rm -f "${KUBEBUILDER_ASSETS}/${binary}"
        curl -fsSL \
            "${KUBERNETES_DOWNLOAD_BASE_URL}/v1.25.16/bin/linux/${arch}/${binary}" \
            --output "${KUBEBUILDER_ASSETS}/${binary}"
        chmod +x "${KUBEBUILDER_ASSETS}/${binary}"
    done
}

main() {
    local arch
    local asset_dir=""
    local actual_version=""
    local cached_assets_valid=false

    arch="$(go env GOARCH)"
    ensure_writable_directory "${KUBEBUILDER_ASSETS}"
    install_setup_envtest

    echo "[INF] Setting up kubebuilder binaries for Kubernetes ${K8S_VERSION}"
    if asset_dir="$(resolve_installed_assets "${arch}" 2>/dev/null)" &&
        assets_are_complete "${asset_dir}" &&
        actual_version="$(kube_apiserver_version "${asset_dir}")"; then
        if version_matches_selector "${actual_version}" "${K8S_VERSION}"; then
            cached_assets_valid=true
        else
            echo "[INF] Cached kube-apiserver version ${actual_version} does not match ${K8S_VERSION}"
        fi
    fi

    if [[ "${cached_assets_valid}" == true ]]; then
        echo "[INF] Reusing cached envtest assets: ${actual_version}"
    else
        echo "[INF] Cached envtest assets are missing or invalid; downloading Kubernetes ${K8S_VERSION}"
        asset_dir="$(download_assets "${arch}")"
        actual_version="$(verify_assets "${asset_dir}")"
        echo "[INF] Repaired envtest assets: ${actual_version}"
    fi

    link_assets "${asset_dir}"
    install_kubernetes_125_cel_fix "${arch}"

    actual_version="$(kube_apiserver_version "${KUBEBUILDER_ASSETS}")"
    if ! version_matches_selector "${actual_version}" "${K8S_VERSION}"; then
        error "requested Kubernetes version: ${K8S_VERSION}"
        error "final kube-apiserver version: ${actual_version}"
        return 1
    fi
    echo "[INF] Verified envtest kube-apiserver: ${actual_version}"
}

main "$@"
