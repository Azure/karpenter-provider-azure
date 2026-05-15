#!/usr/bin/env bash
# Wrapper script for go that sets CC for CGO cross-compilation
# Used by ko via KO_GO_PATH environment variable

# This exists because ko sets GOARCH when building a particular architecture of a multi-arch image,
# but does not set CC, which is required for cross-architecture CGO_ENABLED=1 builds.

set -euo pipefail

# Set CC based on target architecture for CGO cross-compilation
case "${GOARCH:-}" in
    amd64)
        export CC="${CC:-x86_64-linux-gnu-gcc}"
        ;;
    arm64)
        export CC="${CC:-aarch64-linux-gnu-gcc}"
        ;;
esac

exec go "$@"
