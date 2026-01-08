FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.11-bookworm@sha256:2628df1741a14ef60cdadd48f87362630719e13a3cc80d3785dc331d0c802501

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
