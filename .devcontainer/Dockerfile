FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.5-bookworm@sha256:39cd238cb6dcfdc12109ccf614f71c013f48e581bdfc98bbfc774254c7085e90

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
