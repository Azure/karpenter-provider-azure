FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:c1fd7cad39f3f1e669cab9f9905bc0abbc3557c338065f43daa49940f6b99559

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
