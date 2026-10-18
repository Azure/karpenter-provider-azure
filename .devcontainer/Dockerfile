FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:91e046cbf8a47a056e5a3c59b40d5a6d40aa7ad70c8657107212ccd5b3d11029

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
