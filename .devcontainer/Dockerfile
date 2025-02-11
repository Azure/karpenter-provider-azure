FROM mcr.microsoft.com/oss/go/microsoft/golang:1.23.6-bookworm@sha256:2d8c09c93157b49f2f8f0523d907620d022e6d354e670b60a83125067f0c8960

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
