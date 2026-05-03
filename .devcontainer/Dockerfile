FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:61e607875d60ae21a7a4a49110fe7098355473fbc74ab13091e3c1160cc92f18

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
