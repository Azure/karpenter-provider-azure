FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:be14ce4ce048adf5584ce3a0919d21987284e2235b6e7e9bb0e6a01133870be9

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
