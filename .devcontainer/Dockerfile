FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:eb89c44ae1b5bdf97e3159a82c636f13c230620371bce16fc8c787ba64df9665

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
