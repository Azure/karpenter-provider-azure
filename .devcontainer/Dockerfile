FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:cb00b4361ce1de6c2dba2ef4f94f163b95ab37494e6ce0a2e4d44256f6430759

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
