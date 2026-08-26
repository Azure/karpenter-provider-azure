#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(dirname "$(realpath "$0")")
REPO_ROOT=$(dirname "$SCRIPT_DIR")
use_env_cache=false
all_modules=false
while (( $# )); do
    case "$1" in
        --use-env-cache)
            use_env_cache=true
            shift
            ;;
        --all-modules)
            all_modules=true
            shift
            ;;
        --)
            shift
            break
            ;;
        *)
            break
            ;;
    esac
done

if [[ "$use_env_cache" == true ]]; then
    cache="${GOLANGCI_LINT_CACHE:-}"
else
    cache=$(cd "$REPO_ROOT" && "$SCRIPT_DIR/golangci-cache-path.sh")
fi
if [[ -z "$cache" || "$cache" != /* ]]; then
    echo "unable to resolve an absolute golangci-lint cache path" >&2
    exit 1
fi
export GOLANGCI_LINT_CACHE="$cache"

if [[ "$all_modules" == false ]]; then
    cd "$REPO_ROOT"
    exec golangci-lint-custom "$@"
fi

mapfile -d '' module_files < <(
    find "$REPO_ROOT" \
        -path "$REPO_ROOT/test" -prune -o \
        -name go.mod -type f -print0 | sort -z
)
if (( ${#module_files[@]} == 0 )); then
    echo "no Go modules found under $REPO_ROOT" >&2
    exit 1
fi
for go_mod in "${module_files[@]}"; do
    module_dir=$(dirname "$go_mod")
    if (cd "$module_dir" && golangci-lint-custom "$@"); then
        continue
    else
        exit_code=$?
        exit "$exit_code"
    fi
done
