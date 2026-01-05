FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.11-bookworm@sha256:2a64b5e6fe54b7c06a0764ff9ab2be8115a002f0d24cefc029e6dd905325edce

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
