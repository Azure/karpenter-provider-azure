FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:17626019c8c99700471114cf9878ecb859746ff02bb099ce9d743e8a53216973

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
