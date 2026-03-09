FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:6342a8e9d0ebff249ec8f2bddb40aa6c0d520dfa96d61408a161ed55c7c7704c

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
