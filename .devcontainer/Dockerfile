FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.0-bookworm@sha256:14dcc42a624a5e736d42c092815e0796dadfbe8456fd96ca6d6f53e91e8e1cba

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
