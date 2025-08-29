FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.6-bookworm@sha256:aad3eb77b1d422f5b11ebf8e9bef04146ff7206ab8c319bab33078a27746e500

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
