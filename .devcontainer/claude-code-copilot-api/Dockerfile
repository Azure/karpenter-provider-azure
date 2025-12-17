FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.11-bookworm@sha256:522d54080ec5358575fd84485ebf81a073e1c5cd6a77fae6dd05d40e6967d2ce

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
