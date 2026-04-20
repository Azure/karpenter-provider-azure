FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:24d0b15c59a25b32e382a52eaf2ac6f2262540753cd9cc541037de24f733d72b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
