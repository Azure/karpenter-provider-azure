FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:f48ce559fd1f028a4b79a68ab451a8251f4090f09959a24384c4bbde4627a702

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
