FROM mcr.microsoft.com/oss/go/microsoft/golang:1.25-bookworm@sha256:6c46ff067bf046ff528529945fa8b9b67553a24d289b524d2242d9e1f430da4b

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
