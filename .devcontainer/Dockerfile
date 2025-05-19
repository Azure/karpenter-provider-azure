FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.3-bookworm@sha256:7e69174761ff7fe6f71bb2adf9754b7741af7b8467bd4ba2e465b88a9f872be1

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
