FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.4-bookworm@sha256:c7171283a29bbeafeaf7abba08c7cd9868a67033537c1cffbdf8ac1d65080b5b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
