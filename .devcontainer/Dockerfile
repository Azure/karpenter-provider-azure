FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.8-bookworm@sha256:3b2d4e2a67c649047ffd710a3c65538335a62d5270daebdcbf9e74065fd0690b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
