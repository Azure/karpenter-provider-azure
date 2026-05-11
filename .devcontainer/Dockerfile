FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:286560499f88a68ff68ce11520271185294251e37411a0881c3777d402e8d017

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
