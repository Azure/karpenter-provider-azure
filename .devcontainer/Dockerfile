FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.12-bookworm@sha256:dde6a50185ca22d3fa958a30de8fdd61574e4a8590ccc29775b9824758b6d340

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
