#!/usr/bin/env bash
set -euo pipefail

HEAD_HASH=$(git rev-parse HEAD)

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
source "${SCRIPT_DIR}/common.sh"
config

# Don't release with a dirty commit!
if [[ "$(git status --porcelain)" != "" ]]; then
    echo "There are uncommitted changes, please commit them before releasing."
    exit 1
fi

snapshot "$HEAD_HASH"