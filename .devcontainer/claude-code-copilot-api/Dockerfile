FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.7-bookworm@sha256:19f56b534b09fbee6d097e407fea800ac8fad5c859ef183d0760ebfdff31f5ab

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
