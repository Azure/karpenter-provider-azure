FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.1-bookworm@sha256:891da5efbc41acceee0a9c0762215b31b0a478aba59a3fd7e9aba9c48ebeec66

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
