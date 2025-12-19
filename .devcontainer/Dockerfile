FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:2bb32ed22948488d8639fb0abf6848585f0981fb88fed7492bb23aefb4c46d0b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
