#!/usr/bin/env bash
set -euo pipefail

config(){
  GITHUB_ACCOUNT="Azure"
  RELEASE_ACR=${RELEASE_ACR:-ksnap.azurecr.io} # will always be ovreridden
  RELEASE_REPO_ACR=${RELEASE_REPO_ACR:-${RELEASE_ACR}/public/aks/karpenter/}
  RELEASE_REPO_MAR=mcr.microsoft.com/aks/karpenter
  SNAPSHOT_ACR=${SNAPSHOT_ACR:-ksnap.azurecr.io}
  SNAPSHOT_REPO_ACR=${SNAPSHOT_REPO_ACR:-${SNAPSHOT_ACR}/karpenter/snapshot/}

  CURRENT_MAJOR_VERSION="0"
  RELEASE_PLATFORM="--platform=linux/amd64,linux/arm64"

  MAIN_GITHUB_ACCOUNT="Azure"
  RELEASE_TYPE_STABLE="stable"
  RELEASE_TYPE_SNAPSHOT="snapshot"
}

# versionData sets all the version properties for the passed release version. It sets the values
# RELEASE_VERSION_MAJOR, RELEASE_VERSION_MINOR, and RELEASE_VERSION_PATCH to be used by other scripts
versionData(){
  local VERSION="$1"
  local VERSION="${VERSION#[vV]}"
  RELEASE_VERSION_MAJOR="${VERSION%%\.*}"
  RELEASE_VERSION_MINOR="${VERSION#*.}"
  RELEASE_VERSION_MINOR="${RELEASE_VERSION_MINOR%.*}"
  RELEASE_VERSION_PATCH="${VERSION##*.}"
  RELEASE_MINOR_VERSION="v${RELEASE_VERSION_MAJOR}.${RELEASE_VERSION_MINOR}"
}

snapshot() {
  RELEASE_VERSION=$1
  echo "Release Type: snapshot
Release Version: ${RELEASE_VERSION}
Commit: $(git rev-parse HEAD)
Helm Chart Version $(helmChartVersion "$RELEASE_VERSION")"

  authenticatePrivateRepo
  buildImages "${SNAPSHOT_REPO_ACR}"
  updateHelmChart
  # not locking artifacts for snapshot releases
  cosignImage "${CONTROLLER_IMG}"
  cosignImage "${CONTROLLER_IMG_NAP}"
  publishHelmChart "karpenter" "${RELEASE_VERSION}" "${SNAPSHOT_REPO_ACR}"
  publishHelmChart "karpenter-crd" "${RELEASE_VERSION}" "${SNAPSHOT_REPO_ACR}"
}

release() {
  RELEASE_VERSION=$1
  echo "Release Type: stable
Release Version: ${RELEASE_VERSION}
Commit: $(git rev-parse HEAD)
Helm Chart Version $(helmChartVersion "$RELEASE_VERSION")"

  authenticatePrivateRepo
  buildImages "${RELEASE_REPO_ACR}"
  updateHelmChart "${RELEASE_REPO_MAR}"
  lockImage "${IMG_REPOSITORY}" "${IMG_TAG}"
  lockImage "${IMG_REPOSITORY}" "${IMG_TAG}-aks"
  cosignImage "${CONTROLLER_IMG}"
  cosignImage "${CONTROLLER_IMG_NAP}"
  updateHelmChart
  publishHelmChart "karpenter" "${RELEASE_VERSION}" "${RELEASE_REPO_ACR}"
  publishHelmChart "karpenter-crd" "${RELEASE_VERSION}" "${RELEASE_REPO_ACR}"
}

authenticatePrivateRepo() {
  az acr login -n "${SNAPSHOT_REPO_ACR}"
}

buildImages() {
  RELEASE_REPO=$1
  # Set the SOURCE_DATE_EPOCH and KO_DATA_DATE_EPOCH values for reproducable builds with timestamps
  # https://ko.build/advanced/faq/

  CONTROLLER_IMG=$(GOFLAGS=${GOFLAGS} \
    SOURCE_DATE_EPOCH=$(git log -1 --format='%ct') KO_DATA_DATE_EPOCH=$(git log -1 --format='%ct') KO_DOCKER_REPO=${RELEASE_REPO} \
    ko publish -B --sbom none -t "${RELEASE_VERSION}" "${RELEASE_PLATFORM}" ./cmd/controller)
  CONTROLLER_IMG_NAP=$(GOFLAGS="${GOFLAGS} -tags=ccp" \
    SOURCE_DATE_EPOCH=$(git log -1 --format='%ct') KO_DATA_DATE_EPOCH=$(git log -1 --format='%ct') KO_DOCKER_REPO=${RELEASE_REPO} \
    ko publish -B --sbom none -t "${RELEASE_VERSION}-aks" "${RELEASE_PLATFORM}" ./cmd/controller)
}

updateHelmChart() {
  HELM_CHART_VERSION=$(helmChartVersion "$RELEASE_VERSION")
  IMG_REPOSITORY=$(echo "$CONTROLLER_IMG" | cut -d "@" -f 1 | cut -d ":" -f 1)
  IMG_TAG=$(echo "$CONTROLLER_IMG" | cut -d "@" -f 1 | cut -d ":" -f 2 -s)
  IMG_DIGEST=$(echo "$CONTROLLER_IMG" | cut -d "@" -f 2)

  local REPO=${1:-$IMG_REPOSITORY} # override the release repo if provided
  yq e -i ".controller.image.repository = \"${REPO}\"" charts/karpenter/values.yaml
  yq e -i ".controller.image.tag = \"${IMG_TAG}\"" charts/karpenter/values.yaml
  yq e -i ".controller.image.digest = \"${IMG_DIGEST}\"" charts/karpenter/values.yaml
  yq e -i ".appVersion = \"${RELEASE_VERSION#v}\"" charts/karpenter/Chart.yaml
  yq e -i ".version = \"${HELM_CHART_VERSION#v}\"" charts/karpenter/Chart.yaml
  yq e -i ".appVersion = \"${RELEASE_VERSION#v}\"" charts/karpenter-crd/Chart.yaml
  yq e -i ".version = \"${HELM_CHART_VERSION#v}\"" charts/karpenter-crd/Chart.yaml
}

lockImage() {
  local IMG_REPOSITORY=$1
  local IMG_TAG=$2
  IMG_REGISTRY=$(echo "$IMG_REPOSITORY" | cut -d "/" -f 1)
  IMG_PATH=$(echo "$IMG_REPOSITORY" | cut -d "/" -f 2-)
	az acr repository update -n "${IMG_REGISTRY}" --image "${IMG_PATH}:${IMG_TAG}" \
    --write-enabled false \
    --delete-enabled false
}

releaseType(){
  RELEASE_VERSION=$1

  if [[ "${RELEASE_VERSION}" == v* ]]; then
    echo "$RELEASE_TYPE_STABLE"
  else
    echo "$RELEASE_TYPE_SNAPSHOT"
  fi
}

helmChartVersion(){
  RELEASE_VERSION=$1
  if [[ $(releaseType "$RELEASE_VERSION") == "$RELEASE_TYPE_STABLE" ]]; then
    echo "$RELEASE_VERSION"
  fi

  if [[ $(releaseType "$RELEASE_VERSION") == "$RELEASE_TYPE_SNAPSHOT" ]]; then
    echo "v${CURRENT_MAJOR_VERSION}-${RELEASE_VERSION}"
  fi
}

buildDate(){
  # Set the SOURCE_DATE_EPOCH and KO_DATA_DATE_EPOCH values for reproducable builds with timestamps
  # https://ko.build/advanced/faq/
  DATE_FMT="+%Y-%m-%dT%H:%M:%SZ"
  SOURCE_DATE_EPOCH=$(git log -1 --format='%ct')
  date --utc --date="@${SOURCE_DATE_EPOCH}" $DATE_FMT 2>/dev/null
}

cosignImage() {
  local image=$1
  COSIGN_EXPERIMENTAL=1 cosign sign \
      -a GIT_HASH="$(git rev-parse HEAD)" \
      -a GIT_VERSION="${RELEASE_VERSION}" \
      -a BUILD_DATE="$(buildDate)" \
      "${image}"
}

publishHelmChart() {
  CHART_NAME=$1
  RELEASE_VERSION=$2
  RELEASE_REPO=$3
  HELM_CHART_VERSION=$(helmChartVersion "$RELEASE_VERSION")
  HELM_CHART_FILE_NAME="${CHART_NAME}-${HELM_CHART_VERSION}.tgz"

  cd charts
  helm dependency update "${CHART_NAME}"
  helm lint "${CHART_NAME}"
  helm package "${CHART_NAME}" --version "$HELM_CHART_VERSION"
  helm push "${HELM_CHART_FILE_NAME}" "oci://${RELEASE_REPO}"
  rm "${HELM_CHART_FILE_NAME}"
  cd ..
}
