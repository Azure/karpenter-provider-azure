FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:60b5397919c9852f0df6faf0f9d073c8c7d2042439922dfa5c56dad61965644a

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
