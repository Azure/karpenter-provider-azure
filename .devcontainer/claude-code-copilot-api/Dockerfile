FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:68db2defa204890b431c331ef5937f5d98408bd1a9649293da735d3fe3a9d56a

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
