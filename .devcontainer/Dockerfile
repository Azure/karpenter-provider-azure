FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:c6bf34143196d35a93f081ab0597550851d5d1c20fa45b2cf48d7d6da587a92c

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
