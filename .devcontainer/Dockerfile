FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:b59897a478f16531b7ff4e5e34f09c98c444bbd6f03c51a3114c913fee1358c0

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
