#!/usr/bin/env bash
set -euo pipefail

if (( $# > 1 )); then
    echo "usage: $0 [tool-name]" >&2
    exit 2
fi

tool_name="${1:-}"
if [[ "$tool_name" == */* ]]; then
    echo "tool name must not contain '/': $tool_name" >&2
    exit 2
fi

go_bin=$(go env GOBIN)
if [[ -z "$go_bin" ]]; then
    go_path=$(go env GOPATH)
    go_path=${go_path%%:*}
    if [[ -z "$go_path" ]]; then
        echo "go env GOPATH returned no paths" >&2
        exit 1
    fi
    go_bin="$go_path/bin"
fi

if [[ -n "$tool_name" ]]; then
    printf '%s/%s\n' "$go_bin" "$tool_name"
else
    printf '%s\n' "$go_bin"
fi
