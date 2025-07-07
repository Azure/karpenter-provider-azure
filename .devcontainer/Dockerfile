FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.4-bookworm@sha256:a0c0c8232848dab4eebf17082841704317e5bda693155b93624115f907383c94

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
