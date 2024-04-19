#!/usr/bin/env bash
set -euo pipefail

echo "in release.sh"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

echo "after source release.sh"

git_tag="$(git describe --exact-match --tags || echo "no tag")"
if [[ "${git_tag}" == "no tag" ]]; then
  echo "Failed to release: commit is untagged"
  exit 1
fi
commit_sha="$(git rev-parse HEAD)"

# Don't release with a dirty commit!
if [[ "$(git status --porcelain)" != "" ]]; then
  echo "There are uncommitted changes, please commit them before releasing."
  exit 1
fi

echo "before release command call"

release "${commit_sha}" "${git_tag#v}"
