FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.2-bookworm@sha256:9d8aa17559fc5676061f4dcc0e8c704e3138bccddc8d15aa2c9f589ae4442307

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
