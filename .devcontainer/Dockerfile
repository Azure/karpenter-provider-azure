FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.2-bookworm@sha256:6a34b5287753306e4d16f8c1e43461d722cf5db431d72957d79b50a287ddb4f9

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
