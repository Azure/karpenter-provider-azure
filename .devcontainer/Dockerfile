FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:2f7f3e209b4b129588a6dd1b1579aa4d79e20e2671ae9ba3482cd37a337c8061

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
