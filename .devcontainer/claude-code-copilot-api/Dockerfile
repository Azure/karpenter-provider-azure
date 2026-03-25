FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:0c8bf18868e61d346f4315ba1ed965cc8092d168f69a9d30729be2a56c4a7b27

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
