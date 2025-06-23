FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.4-bookworm@sha256:d9559f122feadff27b4adb8e491bc073ac9b41a80d6e38c0b0fae7224dfcf512

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
