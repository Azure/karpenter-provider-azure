FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:f39c68713e55b7744bc1afb97f22a0aa5054ff3b451ce6e3cab55dfbd55a0343

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
