FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:8cbcec825ea146632d907d4e35d3f4914fafcb26b62f0b3eb7b37f33fe9c4e1f

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
