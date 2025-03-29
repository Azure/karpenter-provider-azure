FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.1-bookworm@sha256:26d3a939cfc79a9233792a5d22a6e6316d2a248f700ca209af85a3690b18a41f

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
