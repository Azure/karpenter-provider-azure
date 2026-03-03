FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:7f31d02dec6d256dfcfa0c7fcc54f1002f8a0c71d6a6a60740e193abce6a1181

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
