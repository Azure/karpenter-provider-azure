FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.5-bookworm@sha256:08603c70343507465c5ce2893d27214fc094c9286c33a3e62f5b65140d351461

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
