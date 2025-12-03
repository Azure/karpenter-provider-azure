FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.10-bookworm@sha256:7227323f9c71753888932a668c996e96f6f294452aaa5ed6d87ca42e6fec3e3a

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
