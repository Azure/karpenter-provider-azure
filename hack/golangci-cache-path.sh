#!/usr/bin/env bash
set -euo pipefail

if (( $# != 0 )); then
    echo "usage: $0" >&2
    exit 2
fi

git_dir=$(git rev-parse --absolute-git-dir)
if [[ -z "$git_dir" || ! -d "$git_dir" ]]; then
    echo "unable to resolve an absolute Git directory" >&2
    exit 1
fi
printf '%s/golangci-lint-cache\n' "$git_dir"
