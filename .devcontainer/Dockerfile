FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.2-bookworm@sha256:454f8cc216ef973e162c67092ee2eee7c9fe2ed1b2df27c2de1e7c4289d354ec

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
