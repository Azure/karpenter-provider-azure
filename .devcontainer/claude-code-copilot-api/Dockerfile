FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:e1875306f1325265c9d1eef4c5391985cacd04beaf2d6e8b64aac26d77a1028e

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
