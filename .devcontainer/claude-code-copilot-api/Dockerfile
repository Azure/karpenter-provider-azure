FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.9-bookworm@sha256:97f179aa79c5ffd315a57144c3c5dec15b2ddccb8eb8086cc7979388a767a04d

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
