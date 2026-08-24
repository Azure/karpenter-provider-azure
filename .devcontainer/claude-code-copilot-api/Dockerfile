FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:318a81ac4625d27eb7cc7f98c6a37359bff46a2463274c415449b4275f86e831

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
