FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:ff5a59cdf66f45e171e164f41bb3a492f408c50a72488e41cc3d04d03182eb00

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
