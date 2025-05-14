FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.3-bookworm@sha256:d9e8acf8d7fdb518c144fff40ce9cd7edd1c0fa7b0c7bdcb076d388a1ce6bad1

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
