FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.7-bookworm@sha256:08329e0d23110a75f197b91773fd6859060c13ec4418792e36eb6b472e9fef77

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
