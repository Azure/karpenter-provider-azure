FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.1-bookworm@sha256:906e53357bfa2e0d0687c2721cb595bea99f6a4eafdb35a693a54a772f47b825

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
