#!/usr/bin/env bash
set -euo pipefail

RELEASE_ACR=${RELEASE_ACR:-ksnap.azurecr.io} # will always be ovreridden
RELEASE_REPO_ACR=${RELEASE_REPO_ACR:-${RELEASE_ACR}/public/aks/karpenter/}
RELEASE_REPO_MAR=mcr.microsoft.com/aks/karpenter
SNAPSHOT_ACR=${SNAPSHOT_ACR:-ksnap.azurecr.io}
SNAPSHOT_REPO_ACR=${SNAPSHOT_REPO_ACR:-${SNAPSHOT_ACR}/karpenter/snapshot/}

CURRENT_MAJOR_VERSION="0"

snapshot() {
  local commit_sha version helm_chart_version

  commit_sha="${1}"
  version="${commit_sha}"
  helm_chart_version="${CURRENT_MAJOR_VERSION}-${commit_sha}"

  echo "Release Type: snapshot
Release Version: ${version}
Commit: ${commit_sha}
Helm Chart Version ${helm_chart_version}"

  authenticate "${SNAPSHOT_ACR}"
  buildAndPublish "${SNAPSHOT_REPO_ACR}" "${version}" "${helm_chart_version}" "${commit_sha}"
}

release() {
  local commit_sha version helm_chart_version

  commit_sha="${1}"
  version="${2}"
  helm_chart_version="${version}"

  echo "Release Type: stable
Release Version: ${version}
Commit: ${commit_sha}
Helm Chart Version ${helm_chart_version}"

  authenticate "${RELEASE_ACR}"
  buildAndPublish "${RELEASE_REPO_ACR}" "${version}" "${helm_chart_version}" "${commit_sha}" \
    "${RELEASE_REPO_MAR}" # repo override for Helm chart
}

authenticate() {
  local acr

  acr="$1"
  az acr login -n "${acr}"
}

buildAndPublish() {
  local oci_repo version helm_chart_version commit_sha date_epoch build_date img img_repo img_tag img_digest

  oci_repo="${1}"
  version="${2}"
  helm_chart_version="${3}"
  commit_sha="${4}"

  date_epoch="$(dateEpoch)"
  build_date="$(buildDate "${date_epoch}")"

  img="$(GOFLAGS=${GOFLAGS:-} \
    SOURCE_DATE_EPOCH="${date_epoch}" KO_DATA_DATE_EPOCH="${date_epoch}" KO_DOCKER_REPO="${oci_repo}" \
    ko publish -B --sbom none -t "${version}"     ./cmd/controller)"
  img_nap="$(GOFLAGS="${GOFLAGS:-} -tags=ccp" \
    SOURCE_DATE_EPOCH="${date_epoch}" KO_DATA_DATE_EPOCH="${date_epoch}" KO_DOCKER_REPO="${oci_repo}" \
    ko publish -B --sbom none -t "${version}"-aks ./cmd/controller)"

  img_repo="$(echo "${img}" | cut -d "@" -f 1 | cut -d ":" -f 1)"
  img_tag="$(echo "${img}" | cut -d "@" -f 1 | cut -d ":" -f 2 -s)"
  img_digest="$(echo "${img}" | cut -d "@" -f 2)"

  img_registry="$(echo "${img_repo}" | cut -d "/" -f 1)"
  img_path="$(echo "${img_repo}" | cut -d "/" -f 2-)"

  lockImage "${img_registry}" "${img_path}" "${img_tag}"
  lockImage "${img_registry}" "${img_path}" "${img_tag}-aks"

  cosignOciArtifact "${version}" "${commit_sha}" "${build_date}" "${img}"
  cosignOciArtifact "${version}" "${commit_sha}" "${build_date}" "${img_nap}"

  repo=${5:-$img_repo} # override the repo if provided (used for MCR)

  yq e -i ".controller.image.repository = \"${repo}\"" charts/karpenter/values.yaml
  yq e -i ".controller.image.tag = \"${img_tag}\"" charts/karpenter/values.yaml
  yq e -i ".controller.image.digest = \"${img_digest}\"" charts/karpenter/values.yaml

  publishHelmChart "${oci_repo}" "karpenter" "${helm_chart_version}" "${commit_sha}" "${build_date}"
  publishHelmChart "${oci_repo}" "karpenter-crd" "${helm_chart_version}" "${commit_sha}" "${build_date}"
}

lockImage() {
  local img_registry img_path img_tag

  img_registry="$1"
  img_path="$2"
  img_tag="$3"

	az acr repository update -n "${img_registry}" --image "${img_path}:${img_tag}" \
    --write-enabled false \
    --delete-enabled false
}

publishHelmChart() {
  local oci_repo helm_chart version commit_sha build_date helm_chart_artifact helm_chart_digest

  oci_repo="${1}"
  helm_chart="${2}"
  version="${3}"
  commit_sha="${4}"
  build_date="${5}"

  helm_chart_artifact="${helm_chart}-${version}.tgz"

  yq e -i ".appVersion = \"${version}\"" "charts/${helm_chart}/Chart.yaml"
  yq e -i ".version = \"${version}\"" "charts/${helm_chart}/Chart.yaml"

  cd charts
  helm dependency update "${helm_chart}"
  helm lint "${helm_chart}"
  helm package "${helm_chart}" --version "${version}"
  helm push "${helm_chart_artifact}" "oci://${oci_repo}"
  rm "${helm_chart_artifact}"
  cd ..

  helm_chart_digest="$(crane digest "${oci_repo}/${helm_chart}:${version}")"
  cosignOciArtifact "${version}" "${commit_sha}" "${build_date}" "${oci_repo}${helm_chart}:${version}@${helm_chart_digest}"
}

cosignOciArtifact() {
  local version commit_sha build_date artifact

  version="${1}"
  commit_sha="${2}"
  build_date="${3}"
  artifact="${4}"

  cosign sign --yes -a version="${version}" -a commitSha="${commit_sha}" -a buildDate="${build_date}" "${artifact}"
}

dateEpoch() {
  git log -1 --format='%ct'
}

buildDate() {
  local date_epoch

  date_epoch="${1}"

  date -u --date="@${date_epoch}" "+%Y-%m-%dT%H:%M:%SZ" 2>/dev/null
}
