FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.3-bookworm@sha256:80650b2332a819f81744dd1cfa9d693fc6ebc5dcfb13c7e8dea9974be8d62de6

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
