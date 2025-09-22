FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.7-bookworm@sha256:71c9a6ebef3cf93db5a04429bb2c543b099758cd9a432ed1213f3574d47145d0

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
