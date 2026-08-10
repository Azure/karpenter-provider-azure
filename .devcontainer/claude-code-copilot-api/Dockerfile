FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:ea9ee4712afebb8f01e6ce443a0e8be729d6027e4ee4ea650ad4356d3a402373

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
