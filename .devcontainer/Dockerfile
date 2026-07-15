FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:be89027d698a8bcec2f8b6e9af8923f485fad6fde033b91c7d4ad8a70410707b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
