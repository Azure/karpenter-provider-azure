FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:7e9339662bf964ba4bfa6ca572c391239d18ca22b37c11d15cf92adbc9082cd8

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
