FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:8365869fedcec3bd332a8c5704ec55152fb397ded5cfc9e57ade54f998add366

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
