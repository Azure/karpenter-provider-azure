FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:b37511f1b5f3953e38dac9a42d57ba4cc75f1d22027bfce9a0f202c29208f042

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
