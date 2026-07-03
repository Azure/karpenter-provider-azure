FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:6b0914f7a0008fb0fe63e22477610294471cdbe2d773d63887925d2f12cae0c2

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
