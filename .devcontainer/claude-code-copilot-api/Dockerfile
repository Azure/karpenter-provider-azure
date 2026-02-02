FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:90ab6d469e367da2028b54f7fb5835f29cf8e82e03004b31c63fdfd3b7ec85ab

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
