FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.11-bookworm@sha256:911d1e831f87d39ac8e2c1a536ca02284e7cce1d97f33f6e0311be453e193d58

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
