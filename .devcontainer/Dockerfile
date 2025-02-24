FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.0-bookworm@sha256:612a91edb4ff7d4c0e5a51daa3e9f15ac6c16c255a5f7a84d08348b7b30d6f69

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
