FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.5-bookworm@sha256:f59a65c0f184272571147cdfe8d813adfb9b82035575f4a59057f7fe11fb1e03

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
