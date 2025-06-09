FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.4-bookworm@sha256:c57c50ffe7a962999136a0e63fa14af798a32569e60a8c9ea4e18681d553878c

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
