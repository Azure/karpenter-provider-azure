FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.7-bookworm@sha256:cc985215fb5941b9da3a8e518b121fb3459b90a16598aab494e316d77a73090b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
