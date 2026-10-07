FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:d4b8b8f3a73239e62bce33f978de4baf6ead31dac28e25fc619870638f0a99b0

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
