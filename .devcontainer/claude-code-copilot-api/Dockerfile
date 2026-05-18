FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:9cb93c7fc65fcec28b0af140bd4e42510e7553dd67718289dfee2a955986b7e3

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
