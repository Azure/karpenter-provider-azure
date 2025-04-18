FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.2-bookworm@sha256:1bdcf4a46716b0a27fc6e9ec57cdfe585ccb0201aed9965cdf4e7ecfce5fea85

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
