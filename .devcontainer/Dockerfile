FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:6cc34635a99943aa0ab9b8ec4e814c5aa45445d8ab50a5281903f1ca3c51416c

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
