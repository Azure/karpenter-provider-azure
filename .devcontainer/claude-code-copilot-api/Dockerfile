FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:8d219d3f8e6edc46cc6d1f4bec61347560c2bca6ba53c4eae908c542fbc72a65

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
