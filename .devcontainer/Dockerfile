FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.9-bookworm@sha256:6b997a6bbbe5a4535e7b2d44db929532ed9ea0d936703a4288110b3c97835069

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
