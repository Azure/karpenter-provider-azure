FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:e1475da3b0412109e79396c15381dd7fd621198161c8ef426b21ae0ea65db838

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
