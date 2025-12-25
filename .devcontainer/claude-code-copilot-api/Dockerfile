FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.11-bookworm@sha256:ae3aad004e1edc511c854faa568cee928feb0f6ebfe339d0042ef9bd4dc64f62

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
