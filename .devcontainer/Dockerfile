FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.6-bookworm@sha256:04090387119cd45c73b426f036f96dd2c44e3e82acdd27b5d95fdf0c7f36fde4

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
